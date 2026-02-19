package main

import (
	"bufio"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	size := info.Size()

	hasher := sha256.New()
	chunkBuf := make([]byte, FileChunkSize)
	var totalRead int64
	for totalRead < size {
		n, err := f.Read(chunkBuf)
		if n > 0 {
			hasher.Write(chunkBuf[:n])
			totalRead += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
	}
	plaintextChecksum := hasher.Sum(nil)
	code := generateCode()
	numChunks := uint32((size + int64(FileChunkSize) - 1) / int64(FileChunkSize))

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek file: %w", err)
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
	getChunk := func() ([]byte, error) {
		n, err := f.Read(chunkBuf)
		if n > 0 {
			return chunkBuf[:n], nil
		}
		if err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	fmt.Println("info: encrypting and sending in chunks...")
	if err := WriteEncryptedUploadChunked(bw, code, baseName, size, numChunks, plaintextChecksum, getChunk, progress); err != nil {
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

func runClientSecureSend(addr, filePath string) error {
	plaintext, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	if len(plaintext) == 0 {
		return fmt.Errorf("file is empty")
	}
	key := make([]byte, SecureKeySize)
	if _, err := io.ReadFull(crand.Reader, key); err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	plaintextChecksum := sha256.Sum256(plaintext)
	nonce, sealed, err := encryptWithKey(key, plaintext)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	conn, err := dialWithFallback(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	bw := bufio.NewWriterSize(conn, bufSize)
	if err := WriteMessageType(bw, MsgSecureUpload); err != nil {
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
	fmt.Println("info: sending encrypted file...")
	if err := WriteEncryptedBlob(bw, baseName, plaintextChecksum[:], nonce, sealed, progress); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	fmt.Println()

	fmt.Println("info: waiting for server...")
	status, code, err := ReadCodeResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if status != StatusOK {
		return fmt.Errorf("server error")
	}

	fmt.Println()
	fmt.Printf("Code: %s (valid 1 hour)\n", code)
	fmt.Printf("Key (save it – needed to download): %s\n", hex.EncodeToString(key))
	fmt.Println("Without the key the file cannot be decrypted.")
	return nil
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

	formatByte := make([]byte, 1)
	if _, err := io.ReadFull(br, formatByte); err != nil {
		return fmt.Errorf("read format: %w", err)
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

	if formatByte[0] == 0 {
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

	if formatByte[0] == 2 {
		name, plaintextChecksum, nonce, sealed, err := ReadEncryptedBlob(br, progress)
		if err != nil {
			return fmt.Errorf("read encrypted blob: %w", err)
		}
		fmt.Println()
		fmt.Print("Enter key (64 hex characters): ")
		var keyHex string
		if _, err := fmt.Scanln(&keyHex); err != nil {
			return fmt.Errorf("read key: %w", err)
		}
		keyHex = strings.TrimSpace(keyHex)
		if len(keyHex) != 64 {
			return fmt.Errorf("key must be 64 hex characters (32 bytes)")
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return fmt.Errorf("invalid hex key: %w", err)
		}
		plaintext, err := decryptWithKey(key, nonce, sealed)
		if err != nil {
			return fmt.Errorf("decrypt: %w", err)
		}
		sum := sha256.Sum256(plaintext)
		if !checksumEqual(sum[:], plaintextChecksum) {
			return fmt.Errorf("checksum mismatch – wrong key or corrupted data")
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

	name, totalPlainLen, numChunks, plaintextChecksum, err := ReadEncryptedBlobChunkedHeader(br)
	if err != nil {
		return fmt.Errorf("read blob header: %w", err)
	}
	savePath := outputPath
	if savePath == "" {
		savePath = filepath.Base(name)
	}
	if savePath == "" {
		savePath = "downloaded_file"
	}
	out, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("create file %s: %w", savePath, err)
	}
	defer out.Close()
	hasher := sha256.New()
	var downloaded int64
	for i := uint32(0); i < numChunks; i++ {
		chunk, err := ReadEncryptedBlobNextChunk(br, code)
		if err != nil {
			return fmt.Errorf("read chunk: %w", err)
		}
		if _, err := out.Write(chunk); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}
		hasher.Write(chunk)
		downloaded += int64(len(chunk))
		progress(downloaded, int64(totalPlainLen))
	}
	fmt.Println()
	if !checksumEqual(hasher.Sum(nil), plaintextChecksum) {
		return fmt.Errorf("checksum mismatch after decrypt – wrong code or corrupted data")
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
