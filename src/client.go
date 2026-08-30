package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
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
	"runtime"
	"strings"
	"sync"
	"time"

	pgzip "github.com/klauspost/pgzip"
)

const (
	primaryAddressListURL = "https://pastebin.com/raw/BnAAYunN"
	backupAddressListURL  = "https://raw.githubusercontent.com/hdmain/rawuploader/refs/heads/main/address"

	dialTimeout      = 30 * time.Second
	probeTimeout     = 8 * time.Second // download + upload probe (256 KiB each way)
	probeDialTimeout = 1 * time.Second
	bufSize          = 2 * 1024 * 1024   // 2 MB bufio
	tcpBufferSize    = 4 * 1024 * 1024   // 4 MB socket buffers
	maxSecureLoadRAM = 500 * 1024 * 1024 // 500 MB; above this, secure send streams in chunks
)

func formatValidDuration(storageDurationSec uint32) string {
	if storageDurationSec == 0 {
		return "valid 30 min"
	}
	d := time.Duration(storageDurationSec) * time.Second
	if d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "valid 1 day"
		}
		return fmt.Sprintf("valid %d days", days)
	}
	if d >= time.Hour {
		hours := int(d / time.Hour)
		if hours == 1 {
			return "valid 1 hour"
		}
		return fmt.Sprintf("valid %d hours", hours)
	}
	mins := int(d / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("valid %d min", mins)
}

func newParallelGzipWriter(w io.Writer) (*pgzip.Writer, error) {
	gz, err := pgzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	// Use all CPU cores for gzip compression blocks.
	gz.SetConcurrency(1<<20, runtime.NumCPU())
	return gz, nil
}

// prepareSendPath returns the path to send (possibly a temp tar.gz) and an optional cleanup to remove temp file.
// If path is a directory and zip is false, prompts "Pack directory into tar.gz? [y/N]"; if no, returns error.
func prepareSendPath(path string, zipFlag bool) (sendPath string, cleanup func(), err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		if !zipFlag {
			fmt.Print("Pack directory into tar.gz? [y/N] ")
			rd := bufio.NewReader(os.Stdin)
			line, _ := rd.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line != "y" && line != "yes" {
				return "", nil, fmt.Errorf("cannot send directory (use -z or --zip to pack)")
			}
		}
		tmp, err := os.CreateTemp("", "tcpraw-*.tar.gz")
		if err != nil {
			return "", nil, fmt.Errorf("create temp: %w", err)
		}
		printSendPhase(fmt.Sprintf("Compressing with %d CPU cores…", runtime.NumCPU()))
		gz, err := newParallelGzipWriter(tmp)
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, err
		}
		tw := tar.NewWriter(gz)
		baseDir := filepath.Dir(path)
		err = filepath.Walk(path, func(fpath string, fi os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(baseDir, fpath)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			// tar expects path with forward slashes
			rel = filepath.ToSlash(rel)
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				f, err := os.Open(fpath)
				if err != nil {
					return err
				}
				_, err = io.Copy(tw, f)
				f.Close()
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			tw.Close()
			gz.Close()
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, fmt.Errorf("pack directory: %w", err)
		}
		if err := tw.Close(); err != nil {
			gz.Close()
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, err
		}
		if err := gz.Close(); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			return "", nil, err
		}
		return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
	}
	// single file
	if zipFlag {
		tmp, err := os.CreateTemp("", "tcpraw-*.tar.gz")
		if err != nil {
			return "", nil, fmt.Errorf("create temp: %w", err)
		}
		printSendPhase(fmt.Sprintf("Compressing with %d CPU cores…", runtime.NumCPU()))
		gz, err := newParallelGzipWriter(tmp)
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, err
		}
		tw := tar.NewWriter(gz)
		f, err := os.Open(path)
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", nil, err
		}
		info, _ := f.Stat()
		hdr, _ := tar.FileInfoHeader(info, "")
		hdr.Name = filepath.Base(path)
		tw.WriteHeader(hdr)
		io.Copy(tw, f)
		f.Close()
		tw.Close()
		gz.Close()
		tmp.Close()
		return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
	}
	return path, nil, nil
}

// extractTarGz extracts archivePath (tar.gz) into the same directory, then removes the archive.
func extractTarGz(archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip file: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	destDir := filepath.Dir(archivePath)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Safe path: no leading slash or ".." escaping
		name := filepath.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			continue
		}
		target := filepath.Join(destDir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
		}
	}
	f.Close()
	return os.Remove(archivePath)
}

func resolveDownloadPath(outputPath, remoteName string) string {
	if outputPath != "" {
		return outputPath
	}
	if base := filepath.Base(remoteName); base != "" && base != "." {
		return base
	}
	return "downloaded_file"
}

func checksumFileAsync(path string) <-chan []byte {
	done := make(chan []byte, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			done <- nil
			return
		}
		defer f.Close()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			done <- nil
			return
		}
		done <- hasher.Sum(nil)
	}()
	return done
}

func handleExistingDownload(savePath string, expectedChecksum []byte, unzip bool) (bool, error) {
	info, err := os.Stat(savePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}

	actualChecksum := <-checksumFileAsync(savePath)
	if !checksumEqual(actualChecksum, expectedChecksum) {
		return false, nil
	}

	if unzip {
		fmt.Printf("File already exists with matching checksum, extracting only: %s\n", savePath)
		if err := extractTarGz(savePath); err != nil {
			return true, fmt.Errorf("unzip: %w", err)
		}
		fmt.Println("Extracted archive.")
		return true, nil
	}

	fmt.Printf("File already exists with matching checksum: %s\n", savePath)
	return true, nil
}

// serverList: [id 0..9] = "host:port"
func fetchServerList() ([]string, error) {
	// Try primary (Pastebin) first, then fall back to GitHub raw if needed.
	body, err := fetchAddressFromURL(primaryAddressListURL)
	if err != nil || strings.TrimSpace(body) == "" {
		body, err = fetchAddressFromURL(backupAddressListURL)
		if err != nil {
			return nil, err
		}
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

func probeServer(addr string, serverID int, fileSize uint64) (speedBps float64, rtt time.Duration, ok bool) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, probeDialTimeout)
	if err != nil {
		return 0, 0, false
	}
	defer conn.Close()
	rtt = time.Since(start)
	setTCPOptions(conn)
	conn.SetDeadline(time.Now().Add(probeTimeout))

	if err := WriteMessageType(conn, MsgTest); err != nil {
		return 0, rtt, false
	}
	if err := WriteTestRequest(conn, fileSize); err != nil {
		return 0, rtt, false
	}

	var free uint64
	if err := binary.Read(conn, binary.BigEndian, &free); err != nil {
		return 0, rtt, false
	}
	if free < fileSize {
		return 0, rtt, false
	}

	var payloadLen uint32
	if err := binary.Read(conn, binary.BigEndian, &payloadLen); err != nil {
		return 0, rtt, false
	}
	if payloadLen == 0 || payloadLen > 4*1024*1024 {
		return 0, rtt, false
	}

	if _, err := io.CopyN(io.Discard, conn, int64(payloadLen)); err != nil {
		return 0, rtt, false
	}
	// Rank by upload speed — send() is limited by client→server throughput, not download.
	upBuf := make([]byte, 64*1024)
	var wrote int64
	upStart := time.Now()
	for wrote < int64(payloadLen) {
		n := len(upBuf)
		if int64(payloadLen)-wrote < int64(n) {
			n = int(int64(payloadLen) - wrote)
		}
		nn, err := conn.Write(upBuf[:n])
		if err != nil {
			return 0, rtt, false
		}
		wrote += int64(nn)
	}
	elapsed := time.Since(upStart).Seconds()
	if elapsed < 0.0001 {
		elapsed = 0.0001
	}
	return float64(payloadLen) / elapsed, rtt, true
}

func tryServersFromList(fileSize int64) (net.Conn, int, string, error) {
	addrs, err := fetchServerList()
	if err != nil {
		return nil, 0, "", fmt.Errorf("fetch server list: %w", err)
	}

	var fileSizeU uint64
	if fileSize > 0 {
		fileSizeU = uint64(fileSize)
	}

	type cand struct {
		id   int
		addr string
	}
	var list []cand
	for id, addr := range addrs {
		if addr == "" {
			continue
		}
		for _, d := range expandDialAddrs(addr) {
			list = append(list, cand{id, d})
		}
	}
	if len(list) == 0 {
		return nil, 0, "", fmt.Errorf("no server available (none had enough space or all probes failed)")
	}

	type ranked struct {
		id       int
		addr     string
		speedBps float64
		rtt      time.Duration
		bgp      bgpInfo
		score    float64
		ok       bool
	}
	results := make([]ranked, len(list))
	var wg sync.WaitGroup
	for i, c := range list {
		wg.Add(1)
		go func(i int, c cand) {
			defer wg.Done()
			speed, rtt, ok := probeServer(c.addr, c.id, fileSizeU)
			if !ok {
				return
			}
			bgp := lookupBGP(hostOfAddr(c.addr))
			results[i] = ranked{
				id: c.id, addr: c.addr, speedBps: speed, rtt: rtt, bgp: bgp, ok: true,
				score: routeScore(speed, rtt, bgp.Hops),
			}
		}(i, c)
	}
	wg.Wait()

	var best ranked
	for _, r := range results {
		if !r.ok {
			continue
		}
		if !best.ok || r.score > best.score {
			best = r
		}
	}

	if !best.ok {
		return nil, 0, "", fmt.Errorf("no server available (none had enough space or all probes failed)")
	}

	conn, err := dialTuned(best.addr)
	if err != nil {
		return nil, 0, "", err
	}
	return conn, best.id, formatRoute(best.id, best.addr, best.speedBps, best.bgp), nil
}

const benchDurationSec uint16 = 2

type serverStats struct {
	id          int
	addr        string
	pingMs      float64
	free        uint64
	downloadBps float64
	uploadBps   float64
	ok          bool
}

// timeLimitReader returns EOF after until; used to read stream for exactly N seconds.
type timeLimitReader struct {
	r     io.Reader
	until time.Time
}

func (t *timeLimitReader) Read(p []byte) (n int, err error) {
	if time.Now().After(t.until) {
		return 0, io.EOF
	}
	return t.r.Read(p)
}

// countWriter counts bytes written (for measuring download throughput on client side).
type countWriter int64

func (c *countWriter) Write(p []byte) (n int, err error) {
	*c += countWriter(len(p))
	return len(p), nil
}

func runServerBench(addr string, id int, durationSec uint16) (pingMs float64, free uint64, downloadBps, uploadBps float64, err error) {
	pingStart := time.Now()
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer conn.Close()
	setTCPBuffers(conn)
	conn.SetDeadline(time.Now().Add(time.Duration(durationSec)*2*time.Second + 15*time.Second))

	bw := bufio.NewWriterSize(conn, 256*1024)
	if err := WriteMessageType(bw, MsgBench); err != nil {
		return 0, 0, 0, 0, err
	}
	if err := WriteBenchRequest(bw, 0, durationSec); err != nil {
		return 0, 0, 0, 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, 0, 0, 0, err
	}
	r := bufio.NewReaderSize(conn, 256*1024)
	if err := binary.Read(r, binary.BigEndian, &free); err != nil {
		return 0, 0, 0, 0, err
	}
	pingMs = time.Since(pingStart).Seconds() * 1000
	// Read stream for at least durationSec and at least benchMinBytes (better measurement stability),
	// then read 8-byte serverTotal to stay in sync.
	until := time.Now().Add(time.Duration(durationSec) * time.Second)
	var downCount countWriter
	tmp := make([]byte, 64*1024)
	for time.Now().Before(until) || int64(downCount) < benchMinBytes {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, readErr := r.Read(tmp)
		if n > 0 {
			downCount += countWriter(n)
		}
		if readErr != nil {
			if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	var serverTotal uint64
	if err := binary.Read(r, binary.BigEndian, &serverTotal); err != nil {
		return pingMs, free, 0, 0, err
	}
	downElapsed := time.Duration(durationSec) * time.Second
	if downElapsed > 0 {
		// Use client-side count (real received bytes); fallback to serverTotal if count is 0
		if int64(downCount) > 0 {
			downloadBps = float64(downCount) / downElapsed.Seconds()
		} else if serverTotal > 0 {
			downloadBps = float64(serverTotal) / downElapsed.Seconds()
		}
	}

	if err := WriteBenchRequest(bw, 1, durationSec); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	if err := bw.Flush(); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	until = time.Now().Add(time.Duration(durationSec) * time.Second)
	randBuf := make([]byte, 64*1024)
	if _, err := io.ReadFull(crand.Reader, randBuf); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	var upTotal int64
	for time.Now().Before(until) || upTotal < benchMinBytes {
		n, _ := bw.Write(randBuf)
		if n > 0 {
			upTotal += int64(n)
		}
		if err := bw.Flush(); err != nil {
			break
		}
	}
	if err := bw.Flush(); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	if err := binary.Write(conn, binary.BigEndian, uint64(upTotal)); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	var ack uint64
	if err := binary.Read(r, binary.BigEndian, &ack); err != nil {
		return pingMs, free, downloadBps, 0, nil
	}
	sec := time.Duration(durationSec).Seconds()
	if sec > 0 {
		// Use our sent count; if 0, use server ack (bytes server received)
		if upTotal > 0 {
			uploadBps = float64(upTotal) / sec
		} else if ack > 0 {
			uploadBps = float64(ack) / sec
		}
	}
	return pingMs, free, downloadBps, uploadBps, nil
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

func runClientServers() error {
	addrs, err := fetchServerList()
	if err != nil {
		return fmt.Errorf("fetch server list: %w", err)
	}
	var servers []struct {
		id   int
		addr string
	}
	for id, addr := range addrs {
		if addr != "" {
			servers = append(servers, struct {
				id   int
				addr string
			}{id, addr})
		}
	}
	if len(servers) == 0 {
		fmt.Println("No servers in list.")
		return nil
	}
	fmt.Printf("Testing each server (2s download, 2s upload of random data)...\n")
	fmt.Println("(N/A = server unreachable or older version without benchmark – update server and try again)")
	var results []serverStats
	for _, srv := range servers {
		fmt.Printf("  Server %d (%s)...\n", srv.id, srv.addr)
		pingMs, free, downBps, upBps, err := runServerBench(srv.addr, srv.id, benchDurationSec)
		if err != nil {
			fmt.Printf("    error: %v\n", err)
			results = append(results, serverStats{id: srv.id, addr: srv.addr, ok: false})
			continue
		}
		results = append(results, serverStats{
			id: srv.id, addr: srv.addr, pingMs: pingMs, free: free,
			downloadBps: downBps, uploadBps: upBps, ok: true,
		})
	}
	const gb = 1024 * 1024 * 1024
	const mb = 1024 * 1024
	fmt.Println()
	fmt.Printf("%-4s %-24s %10s %12s %14s %14s %10s %6s\n", "ID", "Address", "Ping", "Free", "Download", "Upload", "ASN", "Hops")
	fmt.Printf("%-4s %-24s %10s %12s %14s %14s %10s %6s\n", "--", "-------", "----", "----", "--------", "------", "---", "----")
	for _, s := range results {
		pingStr := "N/A"
		freeStr := "N/A"
		downStr := "N/A"
		upStr := "N/A"
		asnStr := "N/A"
		hopsStr := "N/A"
		if s.ok {
			pingStr = fmt.Sprintf("%.0f ms", s.pingMs)
			freeStr = fmt.Sprintf("%.2f GB", float64(s.free)/float64(gb))
			downStr = fmt.Sprintf("%.2f MB/s", s.downloadBps/float64(mb))
			upStr = fmt.Sprintf("%.2f MB/s", s.uploadBps/float64(mb))
			bgp := lookupBGP(hostOfAddr(s.addr))
			if bgp.ASN != "" {
				asnStr = bgp.ASN
			}
			if bgp.Hops > 0 {
				hopsStr = fmt.Sprintf("%d", bgp.Hops)
			}
		}
		fmt.Printf("%-4d %-24s %10s %12s %14s %14s %10s %6s\n", s.id, s.addr, pingStr, freeStr, downStr, upStr, asnStr, hopsStr)
	}
	return nil
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
	conn, err := dialBestPath(addr)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}
	return conn, nil
}

func setTCPBuffers(conn net.Conn) {
	setTCPOptions(conn)
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

func runClientSend(filePath string, addr string, serverIDHint int, storageDurationSec uint32) error {
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
	if storageDurationSec > 0 && size > LongTermMaxBytes {
		return fmt.Errorf("long-term uploads limited to %d MB", LongTermMaxBytes/(1024*1024))
	}

	baseName := filepath.Base(filePath)
	ui := newSendUI(baseName, size)

	hasher := sha256.New()
	chunkBuf := make([]byte, FileChunkSize)
	ui.Status("checksum")
	ui.beginTransfer()
	var totalRead int64
	for totalRead < size {
		n, err := f.Read(chunkBuf)
		if n > 0 {
			hasher.Write(chunkBuf[:n])
			totalRead += int64(n)
			ui.Progress("checksum", totalRead, size)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			ui.Fail()
			return fmt.Errorf("read file: %w", err)
		}
	}
	ui.FinishProgress(size)
	plaintextChecksum := hasher.Sum(nil)
	numChunks := uint32((size + int64(FileChunkSize) - 1) / int64(FileChunkSize))

	var lastErr error
	for attempt := 0; attempt <= maxRetrains; attempt++ {
		if attempt > 0 {
			ui.Status(fmt.Sprintf("retrain %d", attempt))
		}
		conn, serverID, err := connectSendTarget(addr, serverIDHint, size, ui)
		if err != nil {
			ui.Fail()
			return err
		}
		code := generateCodeWithServerID(serverID)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			closeRetrain(conn)
			ui.Fail()
			return fmt.Errorf("seek file: %w", err)
		}

		wc, watch := wrapWatched(conn, size)
		bw := bufio.NewWriterSize(wc, bufSize)
		if err := WriteMessageType(bw, MsgUpload); err != nil {
			closeRetrain(conn)
			ui.Fail()
			return err
		}
		watch.reset()
		ui.beginTransfer()
		progress := func(sent, total int64) {
			ui.Progress("upload", sent, total)
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
		err = WriteEncryptedUploadChunked(bw, code, baseName, size, storageDurationSec, numChunks, plaintextChecksum, getChunk, progress)
		if err == nil {
			err = bw.Flush()
		}
		if isRetrainable(err) {
			closeRetrain(conn)
			lastErr = err
			continue
		}
		if err != nil {
			closeRetrain(conn)
			ui.Fail()
			return fmt.Errorf("send: %w", err)
		}
		ui.FinishProgress(size)

		ui.Status("confirm")
		status, err := ReadStatus(wc)
		if isRetrainable(err) {
			closeRetrain(conn)
			lastErr = err
			continue
		}
		if err != nil {
			closeRetrain(conn)
			ui.Fail()
			return fmt.Errorf("read response: %w", err)
		}
		conn.Close()

		switch status {
		case StatusOK:
			ui.Done(code, storageDurationSec)
			return nil
		case StatusError:
			ui.Fail()
			return fmt.Errorf("server error")
		default:
			ui.Fail()
			return fmt.Errorf("unknown status: %d", status)
		}
	}
	ui.Fail()
	if lastErr == nil {
		lastErr = errStall
	}
	return fmt.Errorf("send: %w", lastErr)
}

func connectSendTarget(addr string, serverIDHint int, fileSize int64, ui *sendUI) (net.Conn, int, error) {
	if addr != "" {
		ui.Status("connect " + addr)
		conn, err := dialWithFallback(addr)
		return conn, 0, err
	}
	if serverIDHint >= 0 && serverIDHint <= 9 {
		addrs, fetchErr := fetchServerList()
		if fetchErr != nil {
			return nil, 0, fmt.Errorf("fetch server list: %w", fetchErr)
		}
		if addrs[serverIDHint] == "" {
			return nil, 0, fmt.Errorf("server %d not in list", serverIDHint)
		}
		ui.Status(fmt.Sprintf("connect #%d", serverIDHint))
		conn, err := dialWithFallback(addrs[serverIDHint])
		return conn, serverIDHint, err
	}
	ui.Status("probing")
	conn, serverID, summary, err := tryServersFromList(fileSize)
	if err != nil {
		return nil, 0, err
	}
	ui.Status(summary)
	return conn, serverID, nil
}

func runClientSecureSend(filePath string, addr string, serverIDHint int, storageDurationSec uint32) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	size := info.Size()
	if size == 0 {
		return fmt.Errorf("file is empty")
	}
	if storageDurationSec > 0 && size > LongTermMaxBytes {
		return fmt.Errorf("long-term uploads limited to %d MB", LongTermMaxBytes/(1024*1024))
	}

	baseName := filepath.Base(filePath)
	ui := newSendUI(baseName, size)
	ui.Status("secure")

	key := make([]byte, SecureKeySize)
	if _, err := io.ReadFull(crand.Reader, key); err != nil {
		ui.Fail()
		return fmt.Errorf("generate key: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetrains; attempt++ {
		if attempt > 0 {
			ui.Status(fmt.Sprintf("retrain %d", attempt))
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				ui.Fail()
				return fmt.Errorf("seek file: %w", err)
			}
		}
		conn, _, err := connectSendTarget(addr, serverIDHint, size, ui)
		if err != nil {
			ui.Fail()
			return err
		}
		wc, watch := wrapWatched(conn, size)
		bw := bufio.NewWriterSize(wc, bufSize)
		if err = WriteMessageType(bw, MsgSecureUpload); err != nil {
			closeRetrain(conn)
			ui.Fail()
			return err
		}
		watch.reset()

		if size <= maxSecureLoadRAM {
			ui.Status("encrypt")
			plaintext, err := io.ReadAll(f)
			if err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("read file: %w", err)
			}
			plaintextChecksum := sha256.Sum256(plaintext)
			nonce, sealed, err := encryptWithKey(key, plaintext)
			if err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("encrypt: %w", err)
			}
			ui.beginTransfer()
			progress := func(sent, total int64) {
				ui.Progress("upload", sent, total)
			}
			if _, err := bw.Write([]byte{0}); err != nil {
				closeRetrain(conn)
				if isRetrainable(err) {
					lastErr = err
					continue
				}
				ui.Fail()
				return err
			}
			if err := binary.Write(bw, binary.BigEndian, storageDurationSec); err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("write storage duration: %w", err)
			}
			err = WriteEncryptedBlob(bw, baseName, plaintextChecksum[:], nonce, sealed, progress)
			if err == nil {
				err = bw.Flush()
			}
			if isRetrainable(err) {
				closeRetrain(conn)
				lastErr = err
				continue
			}
			if err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("send: %w", err)
			}
			ui.FinishProgress(int64(len(sealed)))
		} else {
			ui.Status("checksum")
			if _, err := bw.Write([]byte{1}); err != nil {
				closeRetrain(conn)
				if isRetrainable(err) {
					lastErr = err
					continue
				}
				ui.Fail()
				return err
			}
			hasher := sha256.New()
			chunkBuf := make([]byte, FileChunkSize)
			ui.beginTransfer()
			var totalRead int64
			for totalRead < size {
				n, err := f.Read(chunkBuf)
				if n > 0 {
					hasher.Write(chunkBuf[:n])
					totalRead += int64(n)
					ui.Progress("checksum", totalRead, size)
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					closeRetrain(conn)
					ui.Fail()
					return fmt.Errorf("read file: %w", err)
				}
			}
			ui.FinishProgress(size)
			plaintextChecksum := hasher.Sum(nil)
			numChunks := uint32((size + int64(FileChunkSize) - 1) / int64(FileChunkSize))
			if err := WriteSecureUploadChunkedHeader(bw, baseName, size, storageDurationSec, numChunks, plaintextChecksum); err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("send header: %w", err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("seek file: %w", err)
			}
			watch.reset()
			ui.beginTransfer()
			var sent int64
			var xerr error
			for sent < size {
				n, err := f.Read(chunkBuf)
				if n > 0 {
					nonce, sealed, encErr := encryptWithKey(key, chunkBuf[:n])
					if encErr != nil {
						xerr = fmt.Errorf("encrypt chunk: %w", encErr)
						break
					}
					if err := WriteChunk(bw, nonce, sealed); err != nil {
						xerr = err
						break
					}
					sent += int64(n)
					ui.Progress("upload", sent, size)
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					xerr = fmt.Errorf("read file: %w", err)
					break
				}
			}
			if xerr == nil {
				xerr = bw.Flush()
			}
			if isRetrainable(xerr) {
				closeRetrain(conn)
				lastErr = xerr
				continue
			}
			if xerr != nil {
				closeRetrain(conn)
				ui.Fail()
				return xerr
			}
			ui.FinishProgress(size)
		}

		ui.Status("confirm")
		status, code, err := ReadCodeResponse(wc)
		if isRetrainable(err) {
			closeRetrain(conn)
			lastErr = err
			continue
		}
		conn.Close()
		if err != nil {
			ui.Fail()
			return fmt.Errorf("read response: %w", err)
		}
		if status != StatusOK {
			ui.Fail()
			return fmt.Errorf("server error")
		}
		ui.DoneSecure(code, hex.EncodeToString(key), storageDurationSec)
		return nil
	}
	ui.Fail()
	if lastErr == nil {
		lastErr = errStall
	}
	return fmt.Errorf("send: %w", lastErr)
}

func runClientGet(code, outputPath string, unzip bool) error {
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

	var (
		skipChunks uint32
		out        *os.File
		hasher     = sha256.New()
		downloaded int64
		ui         *sendUI
		savePath   string
		secureKey  []byte
		format     byte
		headerOK   bool
		numChunks  uint32
		totalPlain int64
		plainSum   []byte
		lastErr    error
	)
	defer func() {
		if out != nil {
			out.Close()
		}
	}()

	waitUI := newRecvUI(code, 0)
	waitUI.Status("waiting")

	for attempt := 0; attempt <= maxRetrains; attempt++ {
		if attempt > 0 {
			waitUI.Status(fmt.Sprintf("retrain %d", attempt))
			if ui != nil {
				ui.Status(fmt.Sprintf("retrain %d", attempt))
			}
		}
		conn, br, fmtByte, serverSkipped, err := openDownload(addr, code, skipChunks)
		if err != nil {
			waitUI.Fail()
			return err
		}

		if !headerOK {
			format = fmtByte
		}

		if format == 0 || format == 2 {
			name, plaintextChecksum, nonce, sealedLen, err := ReadEncryptedBlobHeader(br)
			if err != nil {
				closeRetrain(conn)
				if isRetrainable(err) {
					lastErr = err
					continue
				}
				waitUI.Fail()
				return fmt.Errorf("read encrypted blob header: %w", err)
			}
			savePath = resolveDownloadPath(outputPath, name)
			if handled, err := handleExistingDownload(savePath, plaintextChecksum, unzip); handled || err != nil {
				closeRetrain(conn)
				waitUI.Fail()
				return err
			}
			recv := newRecvUI(name, int64(sealedLen))
			recv.beginTransfer()
			sealed, err := ReadEncryptedBlobBody(br, sealedLen, func(done, total int64) { recv.Progress("download", done, total) })
			closeRetrain(conn)
			if isRetrainable(err) {
				lastErr = err
				continue
			}
			if err != nil {
				recv.Fail()
				return fmt.Errorf("read encrypted blob: %w", err)
			}
			recv.FinishProgress(int64(sealedLen))
			if format == 2 {
				recv.endLive()
				key, err := promptSecureKey()
				if err != nil {
					return err
				}
				plaintext, err := decryptWithKey(key, nonce, sealed)
				if err != nil {
					return fmt.Errorf("decrypt: %w", err)
				}
				sum := sha256.Sum256(plaintext)
				if !checksumEqual(sum[:], plaintextChecksum) {
					return fmt.Errorf("checksum mismatch – wrong key or corrupted data")
				}
				if err := os.WriteFile(savePath, plaintext, 0644); err != nil {
					return fmt.Errorf("write file %s: %w", savePath, err)
				}
				return finishGet(recv, savePath, unzip)
			}
			recv.Status("decrypt")
			plaintext, err := decryptWithCode(code, nonce, sealed)
			if err != nil {
				recv.Fail()
				return fmt.Errorf("decrypt: %w", err)
			}
			actualChecksum := sha256.Sum256(plaintext)
			if !checksumEqual(actualChecksum[:], plaintextChecksum) {
				recv.Fail()
				return fmt.Errorf("checksum mismatch after decrypt – wrong code or corrupted data")
			}
			if err := os.WriteFile(savePath, plaintext, 0644); err != nil {
				recv.Fail()
				return fmt.Errorf("write file %s: %w", savePath, err)
			}
			return finishGet(recv, savePath, unzip)
		}

		// chunked formats 1 and 3
		name, totalPlainLen, nChunks, plaintextChecksum, err := ReadEncryptedBlobChunkedHeader(br)
		if err != nil {
			closeRetrain(conn)
			if isRetrainable(err) {
				lastErr = err
				continue
			}
			waitUI.Fail()
			return fmt.Errorf("read blob header: %w", err)
		}
		if !headerOK {
			savePath = resolveDownloadPath(outputPath, name)
			if handled, err := handleExistingDownload(savePath, plaintextChecksum, unzip); handled || err != nil {
				closeRetrain(conn)
				waitUI.Fail()
				return err
			}
			if format == 3 {
				waitUI.endLive()
				secureKey, err = promptSecureKey()
				if err != nil {
					closeRetrain(conn)
					return err
				}
			}
			out, err = os.Create(savePath)
			if err != nil {
				closeRetrain(conn)
				return fmt.Errorf("create file %s: %w", savePath, err)
			}
			ui = newRecvUI(name, int64(totalPlainLen))
			ui.beginTransfer()
			numChunks = nChunks
			totalPlain = int64(totalPlainLen)
			plainSum = plaintextChecksum
			headerOK = true
		}

		if skipChunks > 0 && !serverSkipped {
			if err := discardDownloadChunks(br, skipChunks, code, format == 3, secureKey); err != nil {
				closeRetrain(conn)
				if isRetrainable(err) {
					lastErr = err
					continue
				}
				ui.Fail()
				return fmt.Errorf("skip chunks: %w", err)
			}
		}

		stall := false
		for i := skipChunks; i < numChunks; i++ {
			var pt []byte
			if format == 3 {
				nonce, sealed, err := ReadChunkRaw(br)
				if err != nil {
					if isRetrainable(err) {
						stall = true
						break
					}
					closeRetrain(conn)
					ui.Fail()
					return fmt.Errorf("read chunk: %w", err)
				}
				pt, err = decryptWithKey(secureKey, nonce, sealed)
				if err != nil {
					closeRetrain(conn)
					ui.Fail()
					return fmt.Errorf("decrypt chunk: %w", err)
				}
			} else {
				pt, err = ReadEncryptedBlobNextChunk(br, code)
				if err != nil {
					if isRetrainable(err) {
						stall = true
						break
					}
					closeRetrain(conn)
					ui.Fail()
					return fmt.Errorf("read chunk: %w", err)
				}
			}
			if _, err := out.Write(pt); err != nil {
				closeRetrain(conn)
				ui.Fail()
				return fmt.Errorf("write chunk: %w", err)
			}
			hasher.Write(pt)
			downloaded += int64(len(pt))
			skipChunks = i + 1
			ui.Progress("download", downloaded, totalPlain)
		}
		closeRetrain(conn)
		if stall {
			lastErr = errStall
			continue
		}
		ui.FinishProgress(totalPlain)
		if !checksumEqual(hasher.Sum(nil), plainSum) {
			ui.Fail()
			if format == 3 {
				return fmt.Errorf("checksum mismatch – wrong key or corrupted data")
			}
			return fmt.Errorf("checksum mismatch after decrypt – wrong code or corrupted data")
		}
		return finishGet(ui, savePath, unzip)
	}
	if ui != nil {
		ui.Fail()
	} else {
		waitUI.Fail()
	}
	if lastErr == nil {
		lastErr = errStall
	}
	return fmt.Errorf("download: %w", lastErr)
}

func promptSecureKey() ([]byte, error) {
	fmt.Print("Enter key (64 hex characters): ")
	var keyHex string
	if _, err := fmt.Scanln(&keyHex); err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	keyHex = strings.TrimSpace(keyHex)
	if len(keyHex) != 64 {
		return nil, fmt.Errorf("key must be 64 hex characters (32 bytes)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex key: %w", err)
	}
	return key, nil
}

func discardDownloadChunks(br io.Reader, n uint32, code string, secure bool, _ []byte) error {
	for i := uint32(0); i < n; i++ {
		if secure {
			if _, _, err := ReadChunkRaw(br); err != nil {
				return err
			}
			continue
		}
		if _, err := ReadEncryptedBlobNextChunk(br, code); err != nil {
			return err
		}
	}
	return nil
}

func openDownload(addr, code string, skipChunks uint32) (conn net.Conn, br *bufio.Reader, format byte, serverSkipped bool, err error) {
	try := func(resume bool) (net.Conn, *bufio.Reader, byte, bool, error) {
		c, e := dialWithFallback(addr)
		if e != nil {
			return nil, nil, 0, false, e
		}
		wc, watch := wrapWatched(c, 0)
		bw := bufio.NewWriterSize(wc, bufSize)
		if resume {
			if e = WriteMessageType(bw, MsgResume); e != nil {
				closeRetrain(c)
				return nil, nil, 0, false, e
			}
			if e = WriteResumeRequest(bw, code, skipChunks); e != nil {
				closeRetrain(c)
				return nil, nil, 0, false, e
			}
		} else {
			if e = WriteMessageType(bw, MsgDownload); e != nil {
				closeRetrain(c)
				return nil, nil, 0, false, e
			}
			if e = WriteDownloadRequest(bw, code); e != nil {
				closeRetrain(c)
				return nil, nil, 0, false, e
			}
		}
		if e = bw.Flush(); e != nil {
			closeRetrain(c)
			return nil, nil, 0, false, e
		}
		r := bufio.NewReaderSize(wc, bufSize)
		st, e := ReadStatus(r)
		if e != nil {
			closeRetrain(c)
			return nil, nil, 0, false, e
		}
		if resume && st != StatusOK {
			closeRetrain(c)
			return nil, nil, 0, false, errStall // trigger fallback
		}
		if st == StatusNotFound {
			closeRetrain(c)
			return nil, nil, 0, false, fmt.Errorf("code unknown or expired (data kept 1 hour)")
		}
		if st != StatusOK {
			closeRetrain(c)
			return nil, nil, 0, false, fmt.Errorf("server error (status %d)", st)
		}
		fb := make([]byte, 1)
		if _, e = io.ReadFull(r, fb); e != nil {
			closeRetrain(c)
			return nil, nil, 0, false, e
		}
		watch.reset()
		return c, r, fb[0], resume, nil
	}

	if skipChunks > 0 {
		c, r, f, skipped, e := try(true)
		if e == nil {
			return c, r, f, skipped, nil
		}
		if !isStall(e) && !strings.Contains(e.Error(), "server error") {
			return nil, nil, 0, false, e
		}
		return try(false)
	}
	return try(false)
}

func finishGet(ui *sendUI, savePath string, unzip bool) error {
	ui.DoneSaved(savePath)
	if unzip {
		if err := extractTarGz(savePath); err != nil {
			return fmt.Errorf("unzip: %w", err)
		}
		printSendPhase("Extracted archive.")
	}
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
