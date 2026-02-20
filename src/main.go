package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

var (
	StorageDuration = 30 * time.Minute
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

	clientSendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	clientGetCmd := flag.NewFlagSet("get", flag.ExitOnError)
	clientGetOut := clientGetCmd.String("o", "", "output file (default: name from server)")

	if len(os.Args) < 2 {
		printUsage()
		printTotalNetworkStorage()
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
		if err := runServer(id, *serverPort, *serverDir, *serverWeb); err != nil {
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
		if err := runClientSend(args[0], addr); err != nil {
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
	case "secure":
		if len(os.Args) < 3 {
			printUsage()
			printTotalNetworkStorage()
			os.Exit(1)
		}
		if os.Args[2] != "send" {
			printUsage()
			printTotalNetworkStorage()
			os.Exit(1)
		}
		args := os.Args[3:]
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: tcpraw secure send <file> [host:port]")
			os.Exit(1)
		}
		addr := ""
		if len(args) >= 2 {
			addr = args[1]
		}
		if err := runClientSecureSend(args[0], addr); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	default:
		printUsage()
		printTotalNetworkStorage()
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

func printUsage() {
	fmt.Println("tcpraw – TCP file send/receive; client generates 6-digit code, data encrypted on server")
	fmt.Println()
	fmt.Println("  server – listen for uploads; store encrypted data")
	fmt.Println("  send   – generate code, encrypt file, upload; you get the 6-digit code")
	fmt.Println("  get    – download by code; decrypt with same code (or with key for secure uploads)")
	fmt.Println("  secure send – encrypt with your own 256-bit key; server assigns code; use get + key to download")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  tcpraw server [-id=0] [-port=9999] [-dir=./data] [-web=8080]")
	fmt.Println("    -id=ID     server id 0–9 (first digit of generated codes); default 0")
	fmt.Println("    -web=PORT serve download page in browser (no client needed)")
	fmt.Println("  tcpraw send <file> [host:port]   (host:port = server not on list)")
	fmt.Println("  tcpraw secure send <file> [host:port]")
	fmt.Println("  tcpraw get <6-digit-code> [-o file]")
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
