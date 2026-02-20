package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const publicIPURL = "https://api.ipify.org"

func getServerPublicIP() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(publicIPURL)
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
	return strings.TrimSpace(string(b)), nil
}

// findServerIDByIP returns (id, true) if a list address matches ourIP, else (0, false).
func findServerIDByIP(ourIP string) (int, bool) {
	addrs, err := fetchServerList()
	if err != nil {
		return 0, false
	}
	for id, hostPort := range addrs {
		if hostPort == "" {
			continue
		}
		host, _, err := net.SplitHostPort(hostPort)
		if err != nil {
			continue
		}
		if host == ourIP {
			return id, true
		}
	}
	return 0, false
}

type StoredBlob struct {
	Name              string
	PlaintextChecksum []byte
	Nonce             []byte
	Sealed            []byte
	Chunks            []EncryptedChunk
	TotalPlainLen     uint64
	NumChunks         uint32
	Chunked           bool
	Secure            bool
	CreatedAt         time.Time
}

const indexFilename = ".index.gob"

type store struct {
	mu               sync.RWMutex
	index            map[string]time.Time
	dataDir          string
	storageDuration  time.Duration
}

func newStore(dataDir string) (*store, error) {
	dataDir = filepath.Clean(dataDir)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	st := &store{index: make(map[string]time.Time), dataDir: dataDir, storageDuration: StorageDuration}
	if err := st.loadIndex(); err != nil {
		return nil, err
	}
	if err := st.removeOrphanBlobs(); err != nil {
		return nil, err
	}
	st.cleanupExpired()
	return st, nil
}

func (s *store) blobPath(code string) string {
	return filepath.Join(s.dataDir, code+".blob")
}

func (s *store) dataPath(code string) string {
	return filepath.Join(s.dataDir, code+".dat")
}

func (s *store) DataDir() string {
	return s.dataDir
}

func (s *store) indexPath() string {
	return filepath.Join(s.dataDir, indexFilename)
}

func (s *store) loadIndex() error {
	f, err := os.Open(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return gob.NewDecoder(f).Decode(&s.index)
}

func (s *store) removeOrphanBlobs() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if (!strings.HasSuffix(name, ".blob") && !strings.HasSuffix(name, ".dat")) || len(name) != CodeLength+5 {
			continue
		}
		code := name[:CodeLength]
		s.mu.RLock()
		_, inIndex := s.index[code]
		s.mu.RUnlock()
		if !inIndex {
			path := filepath.Join(s.dataDir, name)
			os.Remove(path)
		}
	}
	return nil
}

func (s *store) saveIndex() error {
	f, err := os.Create(s.indexPath())
	if err != nil {
		return err
	}
	err = gob.NewEncoder(f).Encode(s.index)
	if cErr := f.Close(); err == nil {
		err = cErr
	}
	return err
}

func (s *store) put(code string, b *StoredBlob) error {
	path := s.blobPath(code)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(b); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	s.mu.Lock()
	s.index[code] = b.CreatedAt
	err = s.saveIndex()
	s.mu.Unlock()
	return err
}

func (s *store) get(code string) (*StoredBlob, bool) {
	s.mu.RLock()
	createdAt, ok := s.index[code]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Since(createdAt) > s.storageDuration {
		return nil, false
	}
	f, err := os.Open(s.blobPath(code))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var b StoredBlob
	if err := gob.NewDecoder(f).Decode(&b); err != nil {
		return nil, false
	}
	return &b, true
}

func (s *store) remove(code string) {
	metaPath := s.blobPath(code)
	dataPath := s.dataPath(code)
	_ = os.Remove(metaPath)
	_ = os.Remove(dataPath)
	s.mu.Lock()
	delete(s.index, code)
	s.saveIndex()
	s.mu.Unlock()
}

func (s *store) cleanupExpired() {
	s.mu.Lock()
	cutoff := time.Now().Add(-s.storageDuration)
	var expired []string
	for code, createdAt := range s.index {
		if createdAt.Before(cutoff) {
			expired = append(expired, code)
		}
	}
	s.mu.Unlock()
	for _, code := range expired {
		s.remove(code)
	}
}

func runServer(serverIDFromFlag int, port, dataDir, webPort string) error {
	serverID := serverIDFromFlag
	if ourIP, err := getServerPublicIP(); err == nil {
		if id, ok := findServerIDByIP(ourIP); ok {
			serverID = id
			fmt.Printf("tcpraw server: auto-detected id=%d (address %s is on the list)\n", serverID, ourIP)
		} else {
			fmt.Println("tcpraw server: running outside main network (your address is not on the list).")
			fmt.Printf("  To send files to this server, specify address manually, e.g.:\n")
			fmt.Printf("  tcpraw send <file> %s:%s\n", ourIP, port)
			fmt.Printf("  tcpraw secure send <file> %s:%s\n", ourIP, port)
		}
	}

	st, err := newStore(dataDir)
	if err != nil {
		return err
	}
	go func() {
		tick := time.NewTicker(CleanupInterval)
		defer tick.Stop()
		for range tick.C {
			st.cleanupExpired()
		}
	}()

	rl := newRateLimiter(RateLimitAttempts, RateLimitWindow, BanDuration)
	if webPort != "" {
		go runWebServer(webPort, st, rl)
		fmt.Printf("tcpraw server: web download page on :%s (open in browser, enter code to download)\n", webPort)
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	fmt.Printf("tcpraw server: id=%d, listening on :%s, data dir %s, blobs kept %v, max %d MB, rate limit %d/%v then %v ban\n",
		serverID, port, dataDir, StorageDuration, MaxBlobSize/(1024*1024), RateLimitAttempts, RateLimitWindow, BanDuration)

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept: %v\n", err)
			continue
		}
		go handleConn(conn, st, rl, serverID)
	}
}

type rlEntry struct {
	count       int
	windowStart time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*rlEntry
	banned   map[string]time.Time
	max      int
	window   time.Duration
	ban      time.Duration
}

func newRateLimiter(maxAttempts int, window, ban time.Duration) *rateLimiter {
	return &rateLimiter{
		attempts: make(map[string]*rlEntry),
		banned:   make(map[string]time.Time),
		max:      maxAttempts,
		window:   window,
		ban:      ban,
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if until, ok := rl.banned[ip]; ok {
		if now.Before(until) {
			return false
		}
		delete(rl.banned, ip)
	}
	e := rl.attempts[ip]
	if e == nil || now.Sub(e.windowStart) > rl.window {
		e = &rlEntry{windowStart: now}
		rl.attempts[ip] = e
	}
	e.count++
	if e.count > rl.max {
		rl.banned[ip] = now.Add(rl.ban)
		delete(rl.attempts, ip)
		return false
	}
	return true
}

func extractIP(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func handleConn(conn net.Conn, st *store, rl *rateLimiter, serverID int) {
	defer conn.Close()
	setTCPBuffers(conn)
	r := bufio.NewReaderSize(conn, bufSize)

	msgType, err := ReadMessageType(r)
	if err != nil {
		if err != io.EOF {
			fmt.Fprintf(os.Stderr, "read type: %v\n", err)
		}
		return
	}

	switch msgType {
	case MsgUpload:
		handleUpload(conn, r, st)
	case MsgDownload:
		handleDownload(conn, r, st, rl)
	case MsgSecureUpload:
		handleSecureUpload(conn, r, st, serverID)
	case MsgTest:
		handleTest(conn, r, st)
	default:
		fmt.Fprintf(os.Stderr, "unknown type: %c\n", msgType)
		SendStatus(conn, StatusError)
	}
}

func handleTest(conn net.Conn, r io.Reader, st *store) {
	fileSize, err := ReadTestRequest(r)
	if err != nil {
		return
	}
	free, err := getDiskFreeSpace(st.DataDir())
	if err != nil {
		free = 0
	}
	if err := binary.Write(conn, binary.BigEndian, free); err != nil {
		return
	}
	payloadSize := uint32(TestPayloadSize)
	if err := binary.Write(conn, binary.BigEndian, payloadSize); err != nil {
		return
	}
	// Send payload (zeros) for bandwidth test
	buf := make([]byte, payloadSize)
	for len(buf) > 0 {
		n, _ := conn.Write(buf)
		if n <= 0 {
			return
		}
		buf = buf[n:]
	}
	// Client may close; we don't check fileSize vs free here, client does
	_ = fileSize
}

func handleUpload(conn net.Conn, r io.Reader, st *store) {
	// Stream read of upload header (chunked) and write encrypted chunks straight to disk.
	codeBuf := make([]byte, CodeLength)
	if _, err := io.ReadFull(r, codeBuf); err != nil {
		if err != io.EOF {
			fmt.Fprintf(os.Stderr, "read code: %v\n", err)
		}
		SendStatus(conn, StatusError)
		return
	}
	code := string(codeBuf)

	var nameLen uint16
	if err := binary.Read(r, binary.BigEndian, &nameLen); err != nil {
		fmt.Fprintf(os.Stderr, "read name len: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	nameBuf := make([]byte, nameLen)
	if _, err := io.ReadFull(r, nameBuf); err != nil {
		fmt.Fprintf(os.Stderr, "read name: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	name := string(nameBuf)

	var totalPlainLen uint64
	if err := binary.Read(r, binary.BigEndian, &totalPlainLen); err != nil {
		fmt.Fprintf(os.Stderr, "read totalPlainLen: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	if MaxBlobSize > 0 && int64(totalPlainLen) > MaxBlobSize {
		fmt.Fprintf(os.Stderr, "upload rejected: blob exceeds max size %d MB\n", MaxBlobSize/(1024*1024))
		SendStatus(conn, StatusError)
		return
	}

	var numChunks uint32
	if err := binary.Read(r, binary.BigEndian, &numChunks); err != nil {
		fmt.Fprintf(os.Stderr, "read numChunks: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}

	plaintextChecksum := make([]byte, sha256.Size)
	if _, err := io.ReadFull(r, plaintextChecksum); err != nil {
		fmt.Fprintf(os.Stderr, "read checksum: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}

	baseName := filepath.Base(name)
	if baseName == "" || strings.Contains(baseName, "..") {
		SendStatus(conn, StatusError)
		return
	}

	fmt.Println("info: receiving encrypted file", baseName)

	dataPath := st.dataPath(code)
	df, err := os.Create(dataPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create data file: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	var plainCount uint64
	for i := uint32(0); i < numChunks; i++ {
		// nonce (12)
		var header [16]byte
		if _, err := io.ReadFull(r, header[:12]); err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "read nonce: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
		// sealedLen (4)
		if _, err := io.ReadFull(r, header[12:16]); err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "read sealedLen: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
		sealedLen := binary.BigEndian.Uint32(header[12:16])
		if sealedLen < 16 {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "invalid sealedLen: %d\n", sealedLen)
			SendStatus(conn, StatusError)
			return
		}
		plainCount += uint64(sealedLen - 16)
		if MaxBlobSize > 0 && int64(plainCount) > MaxBlobSize {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "upload rejected mid-stream: blob exceeds max size %d MB\n", MaxBlobSize/(1024*1024))
			SendStatus(conn, StatusError)
			return
		}
		// Write header to file
		if _, err := df.Write(header[:16]); err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "write header to data file: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
		// Write encrypted chunk
		sealed := make([]byte, sealedLen)
		if _, err := io.ReadFull(r, sealed); err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "read sealed chunk: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
		if _, err := df.Write(sealed); err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "write sealed chunk: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
	}
	if err := df.Close(); err != nil {
		os.Remove(dataPath)
		fmt.Fprintf(os.Stderr, "close data file: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	if plainCount != totalPlainLen {
		os.Remove(dataPath)
		fmt.Fprintf(os.Stderr, "mismatched plaintext length: header=%d, counted=%d\n", totalPlainLen, plainCount)
		SendStatus(conn, StatusError)
		return
	}

	blob := &StoredBlob{
		Name:              baseName,
		PlaintextChecksum: plaintextChecksum,
		TotalPlainLen:     totalPlainLen,
		NumChunks:         numChunks,
		Chunked:           true,
		CreatedAt:         time.Now(),
	}
	if err := st.put(code, blob); err != nil {
		fmt.Fprintf(os.Stderr, "save to disk: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	fmt.Printf("Received: %s (code %s), stored encrypted to disk\n", baseName, code)
	SendStatus(conn, StatusOK)
}

func handleSecureUpload(conn net.Conn, r io.Reader, st *store, serverID int) {
	formatByte := make([]byte, 1)
	if _, err := io.ReadFull(r, formatByte); err != nil {
		SendStatus(conn, StatusError)
		return
	}
	if formatByte[0] == 1 {
		handleSecureUploadChunked(conn, r, st, serverID)
		return
	}
	name, plaintextChecksum, nonce, sealed, err := ReadSecureUpload(r, MaxBlobSize)
	if err != nil {
		if err == ErrBlobTooLarge {
			fmt.Fprintf(os.Stderr, "secure upload rejected: blob exceeds max size %d MB\n", MaxBlobSize/(1024*1024))
		} else if err != io.EOF {
			fmt.Fprintf(os.Stderr, "read secure upload: %v\n", err)
		}
		SendStatus(conn, StatusError)
		return
	}
	baseName := filepath.Base(name)
	if baseName == "" || strings.Contains(baseName, "..") {
		SendStatus(conn, StatusError)
		return
	}
	code := generateCodeWithServerID(serverID)
	blob := &StoredBlob{
		Name:              baseName,
		PlaintextChecksum: plaintextChecksum,
		Nonce:             nonce,
		Sealed:            sealed,
		Secure:            true,
		CreatedAt:         time.Now(),
	}
	if err := st.put(code, blob); err != nil {
		fmt.Fprintf(os.Stderr, "save secure blob: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	fmt.Printf("Secure upload: %s (code %s)\n", baseName, code)
	if err := SendCodeResponse(conn, StatusOK, code); err != nil {
		return
	}
}

func handleSecureUploadChunked(conn net.Conn, r io.Reader, st *store, serverID int) {
	name, totalPlainLen, numChunks, plaintextChecksum, err := ReadSecureUploadChunkedHeader(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read secure chunked header: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	if MaxBlobSize > 0 && int64(totalPlainLen) > MaxBlobSize {
		fmt.Fprintf(os.Stderr, "secure chunked upload rejected: exceeds max size %d MB\n", MaxBlobSize/(1024*1024))
		SendStatus(conn, StatusError)
		return
	}
	baseName := filepath.Base(name)
	if baseName == "" || strings.Contains(baseName, "..") {
		SendStatus(conn, StatusError)
		return
	}
	code := generateCodeWithServerID(serverID)
	dataPath := st.dataPath(code)
	df, err := os.Create(dataPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create data file: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	for i := uint32(0); i < numChunks; i++ {
		nonce, sealed, err := ReadChunkRaw(r)
		if err != nil {
			df.Close()
			os.Remove(dataPath)
			fmt.Fprintf(os.Stderr, "read secure chunk: %v\n", err)
			SendStatus(conn, StatusError)
			return
		}
		if _, err := df.Write(nonce); err != nil {
			df.Close()
			os.Remove(dataPath)
			SendStatus(conn, StatusError)
			return
		}
		if err := binary.Write(df, binary.BigEndian, uint32(len(sealed))); err != nil {
			df.Close()
			os.Remove(dataPath)
			SendStatus(conn, StatusError)
			return
		}
		if _, err := df.Write(sealed); err != nil {
			df.Close()
			os.Remove(dataPath)
			SendStatus(conn, StatusError)
			return
		}
	}
	if err := df.Close(); err != nil {
		os.Remove(dataPath)
		SendStatus(conn, StatusError)
		return
	}
	blob := &StoredBlob{
		Name:              baseName,
		PlaintextChecksum: plaintextChecksum,
		TotalPlainLen:     totalPlainLen,
		NumChunks:         numChunks,
		Chunked:           true,
		Secure:            true,
		CreatedAt:         time.Now(),
	}
	if err := st.put(code, blob); err != nil {
		os.Remove(dataPath)
		fmt.Fprintf(os.Stderr, "save secure chunked: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}
	fmt.Printf("Secure upload (chunked): %s (code %s)\n", baseName, code)
	if err := SendCodeResponse(conn, StatusOK, code); err != nil {
		return
	}
}

func handleDownload(conn net.Conn, r io.Reader, st *store, rl *rateLimiter) {
	ip := extractIP(conn.RemoteAddr().String())
	if !rl.allow(ip) {
		fmt.Fprintf(os.Stderr, "rate limit / ban: %s\n", ip)
		SendStatus(conn, StatusError)
		return
	}
	code, err := ReadDownloadRequest(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read code: %v\n", err)
		SendStatus(conn, StatusError)
		return
	}

	blob, ok := st.get(code)
	if !ok {
		SendStatus(conn, StatusNotFound)
		return
	}
	if time.Since(blob.CreatedAt) > st.storageDuration {
		st.remove(code)
		SendStatus(conn, StatusNotFound)
		return
	}

	fmt.Println("info: sending encrypted file for code", code)
	if err := SendStatus(conn, StatusOK); err != nil {
		return
	}
	bw := bufio.NewWriterSize(conn, bufSize)
	if blob.Secure && blob.Chunked {
		if _, err := bw.Write([]byte{3}); err != nil {
			return
		}
		if err := sendChunkedFromFile(bw, st.dataPath(code), blob); err != nil {
			fmt.Fprintf(os.Stderr, "send secure chunked: %v\n", err)
			return
		}
	} else if blob.Secure {
		if _, err := bw.Write([]byte{2}); err != nil {
			return
		}
		if err := WriteEncryptedBlob(bw, blob.Name, blob.PlaintextChecksum, blob.Nonce, blob.Sealed, nil); err != nil {
			fmt.Fprintf(os.Stderr, "send secure: %v\n", err)
			return
		}
	} else if blob.Chunked {
		if _, err := bw.Write([]byte{1}); err != nil {
			return
		}
		if err := sendChunkedFromFile(bw, st.dataPath(code), blob); err != nil {
			fmt.Fprintf(os.Stderr, "send (stream): %v\n", err)
			return
		}
	} else if blob.Chunks != nil {
		if _, err := bw.Write([]byte{1}); err != nil {
			return
		}
		if err := WriteEncryptedBlobChunked(bw, blob.Name, blob.PlaintextChecksum, blob.Chunks); err != nil {
			fmt.Fprintf(os.Stderr, "send: %v\n", err)
			return
		}
	} else {
		if _, err := bw.Write([]byte{0}); err != nil {
			return
		}
		if err := WriteEncryptedBlob(bw, blob.Name, blob.PlaintextChecksum, blob.Nonce, blob.Sealed, nil); err != nil {
			fmt.Fprintf(os.Stderr, "send: %v\n", err)
			return
		}
	}
	if err := bw.Flush(); err != nil {
		return
	}
	fmt.Printf("Sent: %s (code %s)\n", blob.Name, code)
}

const webPageHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Download by code</title>
  <style>
    body { font-family: sans-serif; max-width: 360px; margin: 60px auto; padding: 20px; }
    h1 { font-size: 1.3em; }
    input[type="text"] { width: 100%; padding: 12px; font-size: 1.2em; letter-spacing: 0.2em; text-align: center; box-sizing: border-box; }
    button { width: 100%; margin-top: 12px; padding: 12px; font-size: 1em; cursor: pointer; }
    .error { color: #c00; margin-top: 12px; }
    .hint { color: #666; font-size: 0.9em; margin-top: 8px; }
  </style>
</head>
<body>
  <h1>Download file</h1>
  <p class="hint">Enter the 6-digit code you received.</p>
  <form action="/get" method="GET">
    <input type="text" name="code" placeholder="000000" maxlength="6" pattern="[0-9]{6}" required autofocus>
    <button type="submit">Download</button>
  </form>
  <p id="err" class="error"></p>
  <script>
    var params = new URLSearchParams(location.search);
    if (params.get('err')) document.getElementById('err').textContent = params.get('err');
  </script>
</body>
</html>
`

func runWebServer(port string, st *store, rl *rateLimiter) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(webPageHTML))
	})
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r.RemoteAddr)
		if !rl.allow(ip) {
			http.Redirect(w, r, "/?err=Too+many+attempts.+Try+again+later.", http.StatusFound)
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if len(code) != CodeLength {
			http.Redirect(w, r, "/?err=Invalid+code+(must+be+6+digits)", http.StatusFound)
			return
		}
		blob, ok := st.get(code)
		if !ok {
			http.Redirect(w, r, "/?err=Code+not+found+or+expired", http.StatusFound)
			return
		}
		if time.Since(blob.CreatedAt) > StorageDuration {
			st.remove(code)
			http.Redirect(w, r, "/?err=Code+expired", http.StatusFound)
			return
		}
		if blob.Secure {
			http.Redirect(w, r, "/?err=Secure+upload.+Use+tcpraw+get+with+your+key+to+download.", http.StatusFound)
			return
		}
		safeName := blob.Name
		if safeName == "" || strings.Contains(safeName, "..") || strings.Contains(safeName, "/") {
			safeName = "download"
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(safeName, "\"", "")+"\"")
		w.Header().Set("Content-Type", "application/octet-stream")
		if blob.Chunked {
			df, err := os.Open(st.dataPath(code))
			if err != nil {
				http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
				return
			}
			defer df.Close()
			for i := uint32(0); i < blob.NumChunks; i++ {
				var nonce [12]byte
				if _, err := io.ReadFull(df, nonce[:]); err != nil {
					http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
					return
				}
				var sealedLen uint32
				if err := binary.Read(df, binary.BigEndian, &sealedLen); err != nil {
					http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
					return
				}
				sealed := make([]byte, sealedLen)
				if _, err := io.ReadFull(df, sealed); err != nil {
					http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
					return
				}
				pt, err := decryptChunk(code, nonce[:], sealed)
				if err != nil {
					http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
					return
				}
				if _, err := w.Write(pt); err != nil {
					return
				}
			}
			return
		}
		var plaintext []byte
		if blob.Chunks != nil {
			for _, c := range blob.Chunks {
				pt, err := decryptChunk(code, c.Nonce[:], c.Sealed)
				if err != nil {
					http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
					return
				}
				plaintext = append(plaintext, pt...)
			}
		} else {
			var err error
			plaintext, err = decryptWithCode(code, blob.Nonce, blob.Sealed)
			if err != nil {
				http.Redirect(w, r, "/?err=Decrypt+failed", http.StatusFound)
				return
			}
		}
		w.Write(plaintext)
	})
	addr := ":" + port
	fmt.Fprintf(os.Stderr, "web server listen %s: %v\n", addr, http.ListenAndServe(addr, mux))
}
