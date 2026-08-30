package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const ripeStatBase = "https://stat.ripe.net/data"

var ripeHTTP = &http.Client{Timeout: 5 * time.Second}

type bgpInfo struct {
	ASN  string
	Hops int // shortest AS-path length seen on RIPEstat looking glass; 0 = unknown
}

var bgpCache sync.Map // key: IP string

func hostOfAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func expandDialAddrs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []string{addr}
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return []string{addr}
	}
	seen := make(map[string]struct{}, len(ips))
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		s := ip.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, net.JoinHostPort(s, port))
	}
	if len(out) == 0 {
		return []string{addr}
	}
	return out
}

func lookupBGP(ip string) bgpInfo {
	ip = stripZone(ip)
	if cached, ok := bgpCache.Load(ip); ok {
		return cached.(bgpInfo)
	}
	info := fetchBGP(ip)
	bgpCache.Store(ip, info)
	return info
}

func stripZone(ip string) string {
	if i := strings.IndexByte(ip, '%'); i >= 0 {
		return ip[:i]
	}
	return ip
}

func fetchBGP(ip string) bgpInfo {
	info := bgpInfo{}
	prefix, asn := ripeNetworkInfo(ip)
	info.ASN = asn
	if prefix == "" {
		prefix = ip
	}
	if hops := ripeShortestASPath(prefix); hops > 0 {
		info.Hops = hops
	}
	return info
}

func ripeGet(path, resource string, dest any) error {
	u := fmt.Sprintf("%s/%s/data.json?resource=%s", ripeStatBase, path, resource)
	resp, err := ripeHTTP.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RIPEstat %s: HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dest)
}

func ripeNetworkInfo(ip string) (prefix, asn string) {
	var parsed struct {
		Data struct {
			ASNs   []string `json:"asns"`
			Prefix string   `json:"prefix"`
		} `json:"data"`
	}
	if err := ripeGet("network-info", ip, &parsed); err != nil {
		return "", ""
	}
	prefix = strings.TrimSpace(parsed.Data.Prefix)
	if len(parsed.Data.ASNs) > 0 {
		asn = strings.TrimSpace(parsed.Data.ASNs[0])
	}
	return prefix, asn
}

func ripeShortestASPath(prefix string) int {
	var parsed struct {
		Data struct {
			RRCs []struct {
				Peers []struct {
					ASPath string `json:"as_path"`
				} `json:"peers"`
			} `json:"rrcs"`
		} `json:"data"`
	}
	if err := ripeGet("looking-glass", prefix, &parsed); err != nil {
		return 0
	}
	best := 0
	for _, rrc := range parsed.Data.RRCs {
		for _, p := range rrc.Peers {
			h := asPathHops(p.ASPath)
			if h <= 0 {
				continue
			}
			if best == 0 || h < best {
				best = h
			}
		}
	}
	return best
}

func asPathHops(path string) int {
	n := 0
	for _, f := range strings.Fields(path) {
		f = strings.Trim(f, "{},")
		if f == "" {
			continue
		}
		n++
	}
	return n
}

// routeScore ranks a measured path. Higher is better.
// Throughput dominates; RTT and BGP AS-path length break near-ties.
func routeScore(speedBps float64, rtt time.Duration, hops int) float64 {
	if speedBps <= 0 {
		return 0
	}
	score := speedBps
	if rtt > 0 {
		score /= 1 + float64(rtt.Milliseconds())/2500
	}
	if hops > 1 {
		score /= 1 + 0.07*float64(hops-1)
	}
	return score
}

type dialCandidate struct {
	addr string
	rtt  time.Duration
	bgp  bgpInfo
	conn net.Conn
	err  error
}

// dialBestPath connects to addr, trying every resolved IP and keeping the
// path with the best BGP+RTT score (the one that usually wins after a restart).
func dialBestPath(addr string) (net.Conn, error) {
	targets := expandDialAddrs(addr)
	if len(targets) == 1 {
		return dialTuned(targets[0])
	}

	type result struct {
		c dialCandidate
	}
	ch := make(chan result, len(targets))
	for _, t := range targets {
		go func(target string) {
			start := time.Now()
			conn, err := dialTuned(target)
			rtt := time.Since(start)
			c := dialCandidate{addr: target, rtt: rtt, conn: conn, err: err}
			if err == nil {
				c.bgp = lookupBGP(hostOfAddr(target))
			}
			ch <- result{c}
		}(t)
	}

	var best *dialCandidate
	var bestScore float64
	var lastErr error
	for i := 0; i < len(targets); i++ {
		r := <-ch
		if r.c.err != nil {
			lastErr = r.c.err
			continue
		}
		// Handshake RTT as a stand-in for path quality (no payload yet).
		speed := 1e9 / float64(r.c.rtt.Microseconds()+1)
		sc := routeScore(speed, r.c.rtt, r.c.bgp.Hops)
		if best == nil || sc > bestScore {
			if best != nil && best.conn != nil {
				best.conn.Close()
			}
			cp := r.c
			best = &cp
			bestScore = sc
		} else {
			r.c.conn.Close()
		}
	}
	if best == nil {
		if lastErr != nil {
			return nil, fmt.Errorf("connect to %s: %w", addr, lastErr)
		}
		return nil, fmt.Errorf("connect to %s: no route", addr)
	}
	return best.conn, nil
}

func formatRoute(serverID int, addr string, speedBps float64, bgp bgpInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d", serverID)
	if bgp.ASN != "" {
		fmt.Fprintf(&b, " AS%s", bgp.ASN)
	}
	if bgp.Hops > 0 {
		fmt.Fprintf(&b, " %d hops", bgp.Hops)
	}
	if speedBps > 0 {
		fmt.Fprintf(&b, " %s/s", formatBytes(speedBps))
	}
	_ = addr
	return b.String()
}
