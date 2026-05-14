package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	localDiscoveryPort = 40555
	localDefaultTCP    = 40556
)

func runLocalSender(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	sendPath := filePath
	displayName := filepath.Base(filePath)
	var cleanup func()
	if info.IsDir() {
		// In local mode, directories are auto-packed for transfer.
		packedPath, c, err := prepareSendPath(filePath, true)
		if err != nil {
			return err
		}
		sendPath = packedPath
		cleanup = c
		displayName = filepath.Base(filePath) + ".tar.gz"
		info, err = os.Stat(sendPath)
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return err
		}
	}
	if cleanup != nil {
		defer cleanup()
	}
	peers, _ := scanLocalPeers(1500 * time.Millisecond)
	fmt.Printf("Active clients: %v\n", peers)

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", localDefaultTCP))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
		if err != nil {
			return err
		}
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	size := info.Size()
	fmt.Printf("Sharing file: %s (%d bytes)\n", displayName, size)

	// Broadcast announcement a few times so listeners can catch it.
	for i := 0; i < 3; i++ {
		_ = broadcastLocal(fmt.Sprintf("ANNOUNCE|%s|%d|%d", displayName, size, port))
		time.Sleep(500 * time.Millisecond)
	}
	deadline := time.Now().Add(2 * time.Minute)
	_ = ln.(*net.TCPListener).SetDeadline(deadline)
	fmt.Println("Waiting for local downloads...")
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return err
		}
		go serveLocalFile(conn, sendPath, displayName, size)
	}
	return nil
}

func serveLocalFile(conn net.Conn, path, name string, size int64) {
	defer conn.Close()
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	nameBytes := []byte(name)
	if len(nameBytes) > 65535 {
		nameBytes = nameBytes[:65535]
	}
	bw := bufio.NewWriter(conn)
	_ = binary.Write(bw, binary.BigEndian, uint16(len(nameBytes)))
	_, _ = bw.Write(nameBytes)
	_ = binary.Write(bw, binary.BigEndian, uint64(size))
	_, _ = io.Copy(bw, f)
	_ = bw.Flush()
}

func runLocalReceiver() error {
	pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", localDiscoveryPort))
	if err != nil {
		return err
	}
	defer pc.Close()
	peers, _ := scanLocalPeers(1500 * time.Millisecond)
	fmt.Println("Listening for local file announcements...")
	statusLen := 0
	printActive := func(p []string) {
		s := fmt.Sprintf("Active clients: %v", p)
		if len(s) < statusLen {
			s += strings.Repeat(" ", statusLen-len(s))
		}
		fmt.Printf("\r%s", s)
		statusLen = len(fmt.Sprintf("Active clients: %v", p))
	}
	printActive(peers)
	buf := make([]byte, 2048)
	asked := make(map[string]bool)
	lastScan := time.Now()
	for {
		// Refresh active clients every 15 seconds.
		if time.Since(lastScan) >= 15*time.Second {
			peers, _ = scanLocalPeers(1500 * time.Millisecond)
			printActive(peers)
			lastScan = time.Now()
		}
		_ = pc.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if strings.HasPrefix(msg, "DISCOVER|") {
			replyPort := strings.TrimPrefix(msg, "DISCOVER|")
			host, _, _ := net.SplitHostPort(addr.String())
			_, _ = pc.WriteTo([]byte("HERE|tcpraw"), &net.UDPAddr{IP: net.ParseIP(host), Port: mustAtoi(replyPort)})
			continue
		}
		if !strings.HasPrefix(msg, "ANNOUNCE|") {
			continue
		}
		parts := strings.Split(msg, "|")
		if len(parts) < 4 {
			continue
		}
		name := parts[1]
		size := parts[2]
		port := mustAtoi(parts[3])
		host, _, _ := net.SplitHostPort(addr.String())
		key := host + "|" + name + "|" + size + "|" + strconv.Itoa(port)
		if asked[key] {
			continue
		}
		asked[key] = true
		fmt.Println()
		fmt.Printf("File \"%s\" (%s bytes) is available from %s. Download? (y/n) ", name, size, host)
		in := bufio.NewReader(os.Stdin)
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "y" || line == "yes" {
			if err := downloadLocalFile(host, port, name); err != nil {
				fmt.Printf("download failed: %v\n", err)
			}
		}
		printActive(peers)
	}
}

func downloadLocalFile(host string, port int, suggestedName string) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	var nameLen uint16
	if err := binary.Read(br, binary.BigEndian, &nameLen); err != nil {
		return err
	}
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(br, nameBuf); err != nil {
		return err
	}
	name := filepath.Base(string(nameBuf))
	if name == "" {
		name = filepath.Base(suggestedName)
	}
	var size uint64
	if err := binary.Read(br, binary.BigEndian, &size); err != nil {
		return err
	}
	savePath := uniqueLocalName(name)
	out, err := os.Create(savePath)
	if err != nil {
		return err
	}
	defer out.Close()
	start := time.Now()
	var downloaded int64
	buf := make([]byte, 64*1024)
	remaining := int64(size)
	for remaining > 0 {
		nToRead := len(buf)
		if remaining < int64(nToRead) {
			nToRead = int(remaining)
		}
		n, readErr := io.ReadFull(br, buf[:nToRead])
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return err
			}
			downloaded += int64(n)
			remaining -= int64(n)
			elapsed := time.Since(start).Seconds()
			if elapsed >= 0.001 {
				speed := float64(downloaded) / elapsed
				fmt.Printf("\r  speed: %s/s  |  downloaded: %s  |  left: %s  ",
					formatBytes(speed), formatBytes(float64(downloaded)), formatBytes(float64(remaining)))
			}
		}
		if readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
			return readErr
		}
	}
	fmt.Println()
	if downloaded != int64(size) {
		return fmt.Errorf("incomplete download: got %d of %d bytes", downloaded, size)
	}
	fmt.Printf("Downloaded: %s\n", savePath)
	return nil
}

func uniqueLocalName(base string) string {
	if _, err := os.Stat(base); err != nil {
		return base
	}
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	for i := 1; i < 1000; i++ {
		c := fmt.Sprintf("%s_local_%d%s", name, i, ext)
		if _, err := os.Stat(c); err != nil {
			return c
		}
	}
	return base + ".local"
}

func scanLocalPeers(timeout time.Duration) ([]string, error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	lp := pc.LocalAddr().(*net.UDPAddr).Port
	if err := broadcastLocal(fmt.Sprintf("DISCOVER|%d", lp)); err != nil {
		return nil, err
	}
	_ = pc.SetReadDeadline(time.Now().Add(timeout))
	seen := map[string]bool{}
	buf := make([]byte, 1024)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		if strings.HasPrefix(string(buf[:n]), "HERE|") {
			host, _, _ := net.SplitHostPort(addr.String())
			seen[host] = true
		}
	}
	var out []string
	for ip := range seen {
		out = append(out, ip)
	}
	if len(out) == 0 {
		out = []string{}
	}
	return out, nil
}

func broadcastLocal(msg string) error {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4bcast, Port: localDiscoveryPort})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(msg))
	return err
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
