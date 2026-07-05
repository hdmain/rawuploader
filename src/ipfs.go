package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	IPFSGatewayBase = "https://dweb.link/ipfs/"
	IPFSPinDuration = 30 * time.Minute
	ipfsAPIPort     = "5001"
)

const (
	IPFSStateNone      = 0
	IPFSStateQueued    = 1
	IPFSStateUploading = 2
	IPFSStatePinned    = 3
	IPFSStateFailed    = 4
	IPFSStateExpired   = 5
)

const ipfsIndexFilename = ".ipfs.gob"

type ipfsJob struct {
	Code      string
	State     byte
	CID       string
	URL       string
	Error     string
	CreatedAt time.Time
	PinnedAt  time.Time
	ExpiresAt time.Time
}

type ipfsManager struct {
	mu      sync.RWMutex
	jobs    map[string]*ipfsJob
	dataDir string
	repoDir string
	apiURL  string
	cmd     string
}

func newIPFSManager(dataDir string) *ipfsManager {
	repoDir := filepath.Join(dataDir, ".ipfs")
	return &ipfsManager{
		jobs:    make(map[string]*ipfsJob),
		dataDir: dataDir,
		repoDir: repoDir,
		apiURL:  "http://127.0.0.1:" + ipfsAPIPort,
		cmd:     "ipfs",
	}
}

func ipfsStateLabel(state byte) string {
	switch state {
	case IPFSStateQueued:
		return "queued"
	case IPFSStateUploading:
		return "uploading"
	case IPFSStatePinned:
		return "pinned"
	case IPFSStateFailed:
		return "failed"
	case IPFSStateExpired:
		return "expired"
	default:
		return "none"
	}
}

func (m *ipfsManager) loadIndex() error {
	path := filepath.Join(m.dataDir, ipfsIndexFilename)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	var jobs map[string]*ipfsJob
	if err := gob.NewDecoder(f).Decode(&jobs); err != nil {
		return err
	}
	m.mu.Lock()
	m.jobs = jobs
	m.mu.Unlock()
	return nil
}

func (m *ipfsManager) saveIndex() error {
	m.mu.RLock()
	jobs := make(map[string]*ipfsJob, len(m.jobs))
	for k, v := range m.jobs {
		cp := *v
		jobs[k] = &cp
	}
	m.mu.RUnlock()
	path := filepath.Join(m.dataDir, ipfsIndexFilename)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = gob.NewEncoder(f).Encode(jobs)
	if cErr := f.Close(); err == nil {
		err = cErr
	}
	return err
}

func (m *ipfsManager) getJob(code string) (*ipfsJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[code]
	if !ok {
		return nil, false
	}
	cp := *j
	return &cp, true
}

func (m *ipfsManager) setJob(j *ipfsJob) {
	m.mu.Lock()
	m.jobs[j.Code] = j
	m.mu.Unlock()
	_ = m.saveIndex()
}

func (m *ipfsManager) initAndStart() error {
	if err := os.MkdirAll(m.repoDir, 0755); err != nil {
		return fmt.Errorf("create ipfs repo dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(m.repoDir, "config")); os.IsNotExist(err) {
		fmt.Println("tcpraw server: initializing IPFS repository...")
		cmd := exec.Command(m.cmd, "init", "--profile=server")
		cmd.Env = append(os.Environ(), "IPFS_PATH="+m.repoDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ipfs init: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	if m.apiReady() {
		fmt.Println("tcpraw server: IPFS daemon already running")
		return nil
	}
	fmt.Println("tcpraw server: starting IPFS daemon...")
	cmd := exec.Command(m.cmd, "daemon", "--init")
	cmd.Env = append(os.Environ(), "IPFS_PATH="+m.repoDir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ipfs daemon start: %w (is ipfs installed?)", err)
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if m.apiReady() {
			fmt.Println("tcpraw server: IPFS daemon ready")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("IPFS daemon did not become ready within 30s")
}

func (m *ipfsManager) apiReady() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(m.apiURL+"/api/v0/id", "", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *ipfsManager) queueUpload(st *store, code string) error {
	blob, ok := st.get(code)
	if !ok {
		return fmt.Errorf("code not found")
	}
	if blob.Secure {
		return fmt.Errorf("secure uploads cannot be mirrored to IPFS")
	}
	j := &ipfsJob{
		Code:      code,
		State:     IPFSStateQueued,
		CreatedAt: time.Now(),
	}
	m.setJob(j)
	go m.uploadAndPin(st, code)
	return nil
}

func (m *ipfsManager) uploadAndPin(st *store, code string) {
	j, ok := m.getJob(code)
	if !ok {
		return
	}
	j.State = IPFSStateUploading
	m.setJob(j)

	blob, ok := st.get(code)
	if !ok {
		j.State = IPFSStateFailed
		j.Error = "blob expired before IPFS upload"
		m.setJob(j)
		return
	}

	tmp, err := os.CreateTemp(m.dataDir, "ipfs-upload-*.tmp")
	if err != nil {
		j.State = IPFSStateFailed
		j.Error = err.Error()
		m.setJob(j)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := decryptBlobToWriter(st, code, blob, tmp); err != nil {
		tmp.Close()
		j.State = IPFSStateFailed
		j.Error = err.Error()
		m.setJob(j)
		return
	}
	if err := tmp.Close(); err != nil {
		j.State = IPFSStateFailed
		j.Error = err.Error()
		m.setJob(j)
		return
	}

	cid, err := m.addFile(tmpPath, blob.Name)
	if err != nil {
		j.State = IPFSStateFailed
		j.Error = err.Error()
		m.setJob(j)
		return
	}

	if err := m.pin(cid); err != nil {
		j.State = IPFSStateFailed
		j.Error = err.Error()
		m.setJob(j)
		return
	}

	now := time.Now()
	j.State = IPFSStatePinned
	j.CID = cid
	j.URL = IPFSGatewayBase + cid
	j.PinnedAt = now
	j.ExpiresAt = now.Add(IPFSPinDuration)
	j.Error = ""
	m.setJob(j)
	fmt.Printf("IPFS: pinned %s (code %s) CID %s, expires %v\n", blob.Name, code, cid, j.ExpiresAt.Format("15:04:05"))
}

func (m *ipfsManager) addFile(path, filename string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.Close()

	client := &http.Client{Timeout: 0}
	req, err := http.NewRequest(http.MethodPost, m.apiURL+"/api/v0/add?pin=false", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ipfs add HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var result struct {
		Hash string `json:"Hash"`
		Name string `json:"Name"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&result); err != nil {
		return "", fmt.Errorf("parse ipfs add response: %w", err)
	}
	if result.Hash == "" {
		return "", fmt.Errorf("ipfs add returned empty CID")
	}
	return result.Hash, nil
}

func (m *ipfsManager) pin(cid string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(m.apiURL+"/api/v0/pin/add?arg="+cid, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ipfs pin HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (m *ipfsManager) unpin(cid string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(m.apiURL+"/api/v0/pin/rm?arg="+cid, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ipfs unpin HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (m *ipfsManager) cleanupExpired() {
	m.mu.RLock()
	codes := make([]string, 0)
	for code, j := range m.jobs {
		if j.State == IPFSStatePinned && !j.ExpiresAt.IsZero() && time.Now().After(j.ExpiresAt) {
			codes = append(codes, code)
		}
	}
	m.mu.RUnlock()
	for _, code := range codes {
		j, ok := m.getJob(code)
		if !ok || j.CID == "" {
			continue
		}
		if err := m.unpin(j.CID); err != nil {
			fmt.Fprintf(os.Stderr, "IPFS unpin %s (code %s): %v\n", j.CID, code, err)
		} else {
			fmt.Printf("IPFS: unpinned %s (code %s)\n", j.CID, code)
		}
		j.State = IPFSStateExpired
		j.URL = ""
		m.setJob(j)
	}
}
