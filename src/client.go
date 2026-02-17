package main

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fallbackAddrURL = "https://raw.githubusercontent.com/hdmain/rawuploader/refs/heads/main/address"
	dialTimeout     = 30 * time.Second
	bufSize         = 2 * 1024 * 1024 // 2 MB bufio for throughput
	tcpBufferSize   = 4 * 1024 * 1024 // 4 MB socket buffers for high BDP links
)

func dialWithFallback(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err == nil {
		return conn, nil
	}
	fallback, fetchErr := fetchAddressFromURL(fallbackAddrURL)
	if fetchErr != nil {
		return nil, fmt.Errorf("connect to %s: %w (fallback fetch failed: %v)", addr, err, fetchErr)
	}
	fallback = strings.TrimSpace(fallback)
	if idx := strings.IndexByte(fallback, '\n'); idx >= 0 {
		fallback = strings.TrimSpace(fallback[:idx])
	}
	if fallback == "" {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	fmt.Fprintf(os.Stderr, "info: trying fallback address %s\n", fallback)
	conn, err2 := net.DialTimeout("tcp", fallback, dialTimeout)
	if err2 != nil {
		return nil, fmt.Errorf("connect to %s: %w (fallback %s: %v)", addr, err, fallback, err2)
	}
	setTCPBuffers(conn)
	return conn, nil
}

func setTCPBuffers(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(tcpBufferSize)
		tc.SetWriteBuffer(tcpBufferSize)
	}
}

func fetchAddressFromURL(url string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func generateCode() string {
	return fmt.Sprintf("%06d", 100000+rand.Intn(900000))
}

func runClientSend(addr, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file")
	}

	plaintext, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	plaintextChecksum := sha256.Sum256(plaintext)
	code := generateCode()

	fmt.Println("info: encrypting with your code...")
	nonce, sealed, err := encryptWithCode(code, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	conn, err := dialWithFallback(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	bw := bufio.NewWriterSize(conn, bufSize)
	if err := WriteMessageType(bw, MsgUpload); err != nil {
		return err
	}
	baseName := filepath.Base(filePath)
	start := time.Now()
	progress := func(sent, total int64) {
		elapsed := time.Since(start).Seconds()
		if elapsed < 0.001 {
			return
		}
		speed := float64(sent) / elapsed
		remaining := total - sent
		fmt.Printf("\r  speed: %s/s  |  sent: %s  |  left: %s  ", formatBytes(speed), formatBytes(float64(sent)), formatBytes(float64(remaining)))
	}
	if err := WriteEncryptedUpload(bw, code, baseName, plaintextChecksum[:], nonce, sealed, progress); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	fmt.Println()
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	fmt.Println("info: waiting for server...")
	status, err := ReadStatus(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	switch status {
	case StatusOK:
		fmt.Printf("File sent (encrypted). Your code: %s (valid 1 hour)\n", code)
		return nil
	case StatusError:
		return fmt.Errorf("server error")
	default:
		return fmt.Errorf("unknown status: %d", status)
	}
}

func runClientGet(addr, code, outputPath string) error {
	if len(code) != CodeLength {
		return fmt.Errorf("code must be 6 digits")
	}

	conn, err := dialWithFallback(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	bw := bufio.NewWriterSize(conn, bufSize)
	if err := WriteMessageType(bw, MsgDownload); err != nil {
		return err
	}
	if err := WriteDownloadRequest(bw, code); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}

	fmt.Println("info: waiting for server response...")
	br := bufio.NewReaderSize(conn, bufSize)
	status, err := ReadStatus(br)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if status == StatusNotFound {
		return fmt.Errorf("code unknown or expired (data kept 1 hour)")
	}
	if status != StatusOK {
		return fmt.Errorf("server error (status %d)", status)
	}

	start := time.Now()
	progress := func(downloaded, total int64) {
		elapsed := time.Since(start).Seconds()
		if elapsed < 0.001 {
			return
		}
		speed := float64(downloaded) / elapsed
		remaining := total - downloaded
		fmt.Printf("\r  speed: %s/s  |  downloaded: %s  |  left: %s  ", formatBytes(speed), formatBytes(float64(downloaded)), formatBytes(float64(remaining)))
	}
	name, plaintextChecksum, nonce, sealed, err := ReadEncryptedBlob(br, progress)
	if err != nil {
		return fmt.Errorf("read encrypted blob: %w", err)
	}
	fmt.Println()

	fmt.Println("info: decrypting with your code...")
	plaintext, err := decryptWithCode(code, nonce, sealed)
	if err != nil {
		return fmt.Errorf("decrypt: %w", err)
	}
	actualChecksum := sha256.Sum256(plaintext)
	if !checksumEqual(actualChecksum[:], plaintextChecksum) {
		return fmt.Errorf("checksum mismatch after decrypt – wrong code or corrupted data")
	}

	savePath := outputPath
	if savePath == "" {
		savePath = filepath.Base(name)
	}
	if savePath == "" {
		savePath = "downloaded_file"
	}
	if err := os.WriteFile(savePath, plaintext, 0644); err != nil {
		return fmt.Errorf("write file %s: %w", savePath, err)
	}
	fmt.Printf("Downloaded: %s\n", savePath)
	return nil
}

func formatBytes(b float64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	div, exp := float64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", b/div, "KMGTPE"[exp])
}
