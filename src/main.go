package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

var (
	DefaultServerAddr = "94.249.197.155:9999"
	StorageDuration   = 30 * time.Minute
	CleanupInterval   = 5 * time.Minute
	MaxBlobSize       = int64(15 * 1024 * 1024 * 1024) // 15 GB per upload
	RateLimitAttempts = 50
	RateLimitWindow   = 10 * time.Minute
	BanDuration       = 15 * time.Minute
)

func main() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	serverPort := serverCmd.String("port", "9999", "listen port")
	serverDir := serverCmd.String("dir", "./data", "directory for stored encrypted blobs")
	serverWeb := serverCmd.String("web", "", "web port for browser download page (e.g. 8080); empty = disabled")

	clientSendCmd := flag.NewFlagSet("send", flag.ExitOnError)
	clientGetCmd := flag.NewFlagSet("get", flag.ExitOnError)
	clientGetOut := clientGetCmd.String("o", "", "output file (default: name from server)")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "server":
		_ = serverCmd.Parse(os.Args[2:])
		if err := runServer(*serverPort, *serverDir, *serverWeb); err != nil {
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
		addr := DefaultServerAddr
		if len(args) >= 2 {
			addr = args[1]
		}
		if err := runClientSend(addr, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	case "get":
		_ = clientGetCmd.Parse(os.Args[2:])
		args := clientGetCmd.Args()
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: tcpraw get <6-digit-code> [host:port] [-o file]")
			os.Exit(1)
		}
		addr := DefaultServerAddr
		if len(args) >= 2 {
			addr = args[1]
		}
		code := args[0]
		if err := runClientGet(addr, code, *clientGetOut); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	case "secure":
		if len(os.Args) < 3 {
			printUsage()
			os.Exit(1)
		}
		if os.Args[2] != "send" {
			printUsage()
			os.Exit(1)
		}
		args := os.Args[3:]
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: tcpraw secure send <file> [host:port]")
			os.Exit(1)
		}
		addr := DefaultServerAddr
		if len(args) >= 2 {
			addr = args[1]
		}
		if err := runClientSecureSend(addr, args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "client: %v\n", err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(1)
	}
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
	fmt.Println("  tcpraw server [-port=9999] [-dir=./data] [-web=8080]")
	fmt.Println("    -web=PORT  serve download page in browser (no client needed)")
	fmt.Println("  tcpraw send <file> [host:port]")
	fmt.Println("  tcpraw secure send <file> [host:port]")
	fmt.Println("  tcpraw get <6-digit-code> [host:port] [-o file]")
	fmt.Println()
	fmt.Printf("Default host:port is %s (change DefaultServerAddr in main.go)\n", DefaultServerAddr)
	fmt.Printf("Data kept %v, cleanup every %v, max upload %d MB, rate limit %d codes/%v then %v ban\n",
		StorageDuration, CleanupInterval, MaxBlobSize/(1024*1024), RateLimitAttempts, RateLimitWindow, BanDuration)
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  tcpraw server -port=9999")
	fmt.Println("  tcpraw send document.pdf")
	fmt.Println("  tcpraw get 482917 -o myfile.pdf")
}
