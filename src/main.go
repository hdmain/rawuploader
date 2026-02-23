package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type secureSendArgs struct {
	file     string
	addr     string
	serverID int
}

func parseSecureSendArgs(raw []string) secureSendArgs {
	var out secureSendArgs
	out.serverID = -1
	var positional []string
	for i := 0; i < len(raw); i++ {
		s := raw[i]
		if s == "-server" && i+1 < len(raw) {
			id, _ := strconv.Atoi(raw[i+1])
			if id >= 0 && id <= 9 {
				out.serverID = id
			}
			i++
			continue
		}
		if strings.HasPrefix(s, "-server=") {
			id, _ := strconv.Atoi(strings.TrimPrefix(s, "-server="))
			if id >= 0 && id <= 9 {
				out.serverID = id
			}
			continue
		}
		positional = append(positional, s)
	}
	if len(positional) >= 1 {
		out.file = positional[0]
	}
	if len(positional) >= 2 {
		out.addr = positional[1]
	}
	return out
}

const versionURL = "https://raw.githubusercontent.com/hdmain/rawuploader/main/version"

// Version – change only here; remote check uses GitHub raw version file.
var Version = "1.1.6"

var (
	StorageDuration   = 30 * time.Minute
	CleanupInterval   = 5 * time.Minute
	MaxBlobSize       = int64(15 * 1024 * 1024 * 1024) // 15 GB per upload
	RateLimitAttempts = 50
	RateLimitWindow   = 10 * time.Minute
	BanDuration       = 15 * time.Minute
)

func main() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	serverID := serverCmd.Int("id", 0, "server id 0–9 (first digit of generated codes)")
	serverPort := serverCmd.String("port", "9999", "listen port")
	serverDir := serverCmd.String("dir", "./data", "directory for stored encrypted blobs")
	serverWeb := serverCmd.String("web", "", "web port for browser download page (e.g. 8080); empty = disabled")
	serverMaxSizeMB := serverCmd.Int64("maxsize", 0, "max upload size in MB (0 = use default from code)")

	clientSendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	clientSendServerID := clientSendCmd.Int("server", -1, "server id 0–9 to use (default: auto-probe)")
	clientGetCmd := flag.NewFlagSet("get", flag.ExitOnError)
	clientGetOut := clientGetCmd.String("o", "", "output file (default: name from server)")

	if len(os.Args) < 2 {
		printUsage()
		printTotalNetworkStorage()
		printVersionCheck()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "server":
		_ = serverCmd.Parse(os.Args[2:])
		id := *serverID
		if id < 0 || id > 9 {
			fmt.Fprintln(os.Stderr, "server id must be 0–9")
			os.Exit(1)
		}
		maxBlob := MaxBlobSize
		if *serverMaxSizeMB > 0 {
			maxBlob = *serverMaxSizeMB * 1024 * 1024
		}
		if err := runServer(id, *serverPort, *serverDir, *serverWeb, maxBlob); err != nil {
			fmt.Fprintf(os.Stderr, "server: %v\n", err)
			os.Exit(1)
		}
	case "send":
		_ = clientSendCmd.Parse(os.Args[2:])
		args := clientSendCmd.Args()
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: tcpraw send <file> [host:port]")
			os.Exit(1)
		}
		addr := ""
		if len(args) >= 2 {
			addr = args[1]
		}
		if err := runClientSend(args[0], addr, *clientSendServerID); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	case "get":
		// Extract -o/--output from any position (flag.Parse stops at first non-flag)
		getArgs := os.Args[2:]
		var getOutput string
		var getPositional []string
		for i := 0; i < len(getArgs); i++ {
			switch getArgs[i] {
			case "-o", "--output":
				if i+1 < len(getArgs) {
					getOutput = getArgs[i+1]
					i++
				}
				continue
			}
			getPositional = append(getPositional, getArgs[i])
		}
		_ = clientGetCmd.Parse(getPositional)
		args := clientGetCmd.Args()
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: tcpraw get <6-digit-code> [-o file]")
			os.Exit(1)
		}
		code := args[0]
		outPath := getOutput
		if outPath == "" {
			outPath = *clientGetOut
		}
		if err := runClientGet(code, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	case "servers":
		if err := runClientServers(); err != nil {
			fmt.Fprintf(os.Stderr, "servers: %v\n", err)
			os.Exit(1)
		}
	case "secure":
		if len(os.Args) < 3 {
			printUsage()
			printTotalNetworkStorage()
			printVersionCheck()
			os.Exit(1)
		}
		if os.Args[2] != "send" {
			printUsage()
			printTotalNetworkStorage()
			printVersionCheck()
			os.Exit(1)
		}
		args := parseSecureSendArgs(os.Args[3:])
		if args.file == "" {
			fmt.Fprintln(os.Stderr, "usage: tcpraw secure send <file> [host:port]")
			os.Exit(1)
		}
		if err := runClientSecureSend(args.file, args.addr, args.serverID); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	default:
		printUsage()
		printTotalNetworkStorage()
		printVersionCheck()
		os.Exit(1)
	}
}

func printTotalNetworkStorage() {
	total := getTotalNetworkStorage(3 * time.Second)
	const gb = 1024 * 1024 * 1024
	if total == 0 {
		fmt.Println("Total network storage: N/A")
		return
	}
	gbF := float64(total) / float64(gb)
	fmt.Printf("Total network storage: %.2f GB\n", gbF)
}

func printVersionCheck() {
	remote, err := fetchRemoteVersion(3 * time.Second)
	if err != nil || remote == "" {
		return
	}
	remote = strings.TrimSpace(remote)
	if versionLess(Version, remote) {
		fmt.Printf("New version available: %s (you have %s)\n", remote, Version)
	}
}

func fetchRemoteVersion(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// versionLess returns true if a < b (e.g. "1.1.5" < "1.1.6").
func versionLess(a, b string) bool {
	partsA := strings.Split(strings.TrimSpace(a), ".")
	partsB := strings.Split(strings.TrimSpace(b), ".")
	for i := 0; i < len(partsA) || i < len(partsB); i++ {
		var na, nb int
		if i < len(partsA) {
			na, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			nb, _ = strconv.Atoi(partsB[i])
		}
		if na < nb {
			return true
		}
		if na > nb {
			return false
		}
	}
	return false
}

func printUsage() {
	fmt.Println("tcpraw – TCP file send/receive; client generates 6-digit code, data encrypted on server")
	fmt.Println()
	fmt.Println("  server  – listen for uploads; store encrypted data")
	fmt.Println("  servers – test all servers: free space, ~10s upload & download speed")
	fmt.Println("  send    – generate code, encrypt file, upload; you get the 6-digit code")
	fmt.Println("  get     – download by code; decrypt with same code (or with key for secure uploads)")
	fmt.Println("  secure send – encrypt with your own 256-bit key; server assigns code; use get + key to download")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  tcpraw server [-id=0] [-port=9999] [-dir=./data] [-web=8080] [-maxsize=0]")
	fmt.Println("    -id=ID       server id 0–9 (first digit of generated codes); default 0")
	fmt.Println("    -web=PORT    serve download page in browser (no client needed)")
	fmt.Println("    -maxsize=MB  max upload size in MB (0 = default from code)")
	fmt.Println("  tcpraw send [-server=0-9] <file> [host:port]   (-server = use that server id; host:port = override)")
	fmt.Println("  tcpraw secure send [-server=0-9] <file> [host:port]")
	fmt.Println("  tcpraw get <6-digit-code> [-o file]")
	fmt.Println("  tcpraw servers   (benchmark all servers, ~10s upload+download each)")
	fmt.Println()
	fmt.Println("Servers are read from the address list (first digit of code = server id).")
	fmt.Printf("Data kept %v, cleanup every %v, max upload %d MB, rate limit %d codes/%v then %v ban\n",
		StorageDuration, CleanupInterval, MaxBlobSize/(1024*1024), RateLimitAttempts, RateLimitWindow, BanDuration)
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  tcpraw server -port=9999")
	fmt.Println("  tcpraw send document.pdf")
	fmt.Println("  tcpraw get 482917 -o myfile.pdf")
}
