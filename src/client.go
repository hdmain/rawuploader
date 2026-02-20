package main

import (
	"bufio"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	addressListURL   = "https://raw.githubusercontent.com/hdmain/rawuploader/refs/heads/main/address"
	dialTimeout      = 30 * time.Second
	probeTimeout     = 1 * time.Second
	probeDialTimeout = 500 * time.Millisecond
	bufSize          = 2 * 1024 * 1024 // 2 MB bufio for throughput
	tcpBufferSize    = 4 * 1024 * 1024 // 4 MB socket buffers for high BDP links
)

// serverList: [id 0..9] = "host:port"
func fetchServerList() ([]string, error) {
	body, err := fetchAddressFromURL(addressListURL)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 10)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		idStr := line[:idx]
		hostPort := strings.TrimSpace(line[idx+1:])
		if hostPort == "" {
			continue
		}
		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id < 0 || id > 9 {
			continue
		}
		addrs[id] = hostPort
	}
	// Default server when list is empty or id 0 missing
	if addrs[0] == "" {
		addrs[0] = "94.249.197.155:9999"
	}
	return addrs, nil
}

type probeResult struct {
	serverID int
	addr     string
	speedBps float64
	ok       bool
}

func probeServer(addr string, serverID int, fileSize uint64) (speedBps float64, ok bool) {
	conn, err := net.DialTimeout("tcp", addr, probeDialTimeout)
	if err != nil {
		return 0, false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(probeTimeout))

	if err := WriteMessageType(conn, MsgTest); err != nil {
		return 0, false
	}
	if err := WriteTestRequest(conn, fileSize); err != nil {
		return 0, false
	}

	var free uint64
	if err := binary.Read(conn, binary.BigEndian, &free); err != nil {
		return 0, false
	}
	if free < fileSize {
		return 0, false
	}

	var payloadLen uint32
	if err := binary.Read(conn, binary.BigEndian, &payloadLen); err != nil {
		return 0, false
	}
	if payloadLen == 0 || payloadLen > 4*1024*1024 {
		return 0, false
	}

	start := time.Now()
	n, err := io.CopyN(io.Discard, conn, int64(payloadLen))
	if err != nil || n != int64(payloadLen) {
		return 0, false
	}
	elapsed := time.Since(start).Seconds()
	if elapsed < 0.0001 {
		elapsed = 0.0001
	}
	return float64(payloadLen) / elapsed, true
}

func tryServersFromList(fileSize int64) (net.Conn, int, error) {
	addrs, err := fetchServerList()
	if err != nil {
		return nil, 0, fmt.Errorf("fetch server list: %w", err)
	}

	fileSizeU := uint64(fileSize)
	if fileSizeU < 0 {
		fileSizeU = 0
	}

	var wg sync.WaitGroup
	results := make(chan probeResult, 10)
	for id, addr := range addrs {
		if addr == "" {
			continue
		}
		wg.Add(1)
		go func(serverID int, a string) {
			defer wg.Done()
			speed, ok := probeServer(a, serverID, fileSizeU)
			results <- probeResult{serverID, a, speed, ok}
		}(id, addr)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var best probeResult
	for r := range results {
		if !r.ok {
			continue
		}
		if r.speedBps > best.speedBps {
			best = r
		}
	}

	if !best.ok {
		return nil, 0, fmt.Errorf("no server available (none had enough space or all probes failed)")
	}

	conn, err := net.DialTimeout("tcp", best.addr, dialTimeout)
	if err != nil {
		return nil, 0, err
	}
	setTCPBuffers(conn)
	return conn, best.serverID, nil
}

// getServerFreeSpace returns free disk space (bytes) for one server, or 0 on failure.
func getServerFreeSpace(addr string) uint64 {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return 0
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	defer conn.Close()
	if WriteMessageType(conn, MsgTest) != nil || WriteTestRequest(conn, 0) != nil {
		return 0
	}
	var free uint64
	if binary.Read(conn, binary.BigEndian, &free) != nil {
		return 0
	}
	return free
}

// getTotalNetworkStorage returns sum of free disk space (bytes) across all servers from the list. Timeout applies to the whole operation.
func getTotalNetworkStorage(timeout time.Duration) uint64 {
	addrs, err := fetchServerList()
	if err != nil {
		return 0
	}
	var total uint64
	var mu sync.Mutex
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		for _, addr := range addrs {
			if addr == "" {
				continue
			}
			wg.Add(1)
			go func(a string) {
				defer wg.Done()
				free := getServerFreeSpace(a)
				if free > 0 {
					mu.Lock()
					total += free
					mu.Unlock()
				}
			}(addr)
		}
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return total
	case <-time.After(timeout):
		return total
	}
}

func dialWithFallback(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err == nil {
		setTCPBuffers(conn)
		return conn, nil
	}
	return nil, fmt.Errorf("connect to %s: %w", addr, err)
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
	return generateCodeWithServerID(0)
}

// generateCodeWithServerID – first digit of code = server id (0–9), rest random.
func generateCodeWithServerID(serverID int) string {
	if serverID < 0 || serverID > 9 {
		serverID = 0
	}
	return fmt.Sprintf("%d%05d", serverID, rand.Intn(100000))
}

func runClientSend(filePath string, addr string) error {
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
	var conn net.Conn
	var serverID int
	if addr != "" {
		var err error
		conn, err = dialWithFallback(addr)
		if err != nil {
			return err
		}
		serverID = 0
	} else {
		fmt.Println("info: probing servers (disk space + bandwidth, max 1s)...")
		var err error
		conn, serverID, err = tryServersFromList(size)
		if err != nil {
			return err
		}
	}
	defer conn.Close()
	code := generateCodeWithServerID(serverID)
	numChunks := uint32((size + int64(FileChunkSize) - 1) / int64(FileChunkSize))

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek file: %w", err)
	}

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

func runClientSecureSend(filePath string, addr string) error {
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

	var conn net.Conn
	if addr != "" {
		conn, err = dialWithFallback(addr)
	} else {
		fmt.Println("info: probing servers (disk space + bandwidth, max 1s)...")
		conn, _, err = tryServersFromList(int64(len(plaintext)))
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	bw := bufio.NewWriterSize(conn, bufSize)
	if err = WriteMessageType(bw, MsgSecureUpload); err != nil {
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

func runClientGet(code, outputPath string) error {
	if len(code) != CodeLength {
		return fmt.Errorf("code must be 6 digits")
	}
	serverID := int(code[0] - '0')
	if serverID < 0 || serverID > 9 {
		return fmt.Errorf("invalid code: first digit must be 0–9 (server id)")
	}
	addrs, err := fetchServerList()
	if err != nil {
		return fmt.Errorf("fetch server list: %w", err)
	}
	if addrs[serverID] == "" {
		return fmt.Errorf("server %d not in list", serverID)
	}
	addr := addrs[serverID]
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
