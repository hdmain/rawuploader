package main

import (
	"errors"
	"net"
	"sync"
	"time"
)

var errStall = errors.New("path stalled (retrain)")

func isStall(err error) bool {
	return err != nil && errors.Is(err, errStall)
}

func isRetrainable(err error) bool {
	if isStall(err) {
		return true
	}
	var ne net.Error
	return err != nil && errors.As(err, &ne) && ne.Timeout()
}

const (
	stallWarmup    = 5 * time.Second
	stallWindow    = 2500 * time.Millisecond
	stallHold      = 3 * time.Second
	stallRatio     = 0.22
	stallMinPeak   = 128 * 1024 // B/s; below this the link is just slow
	maxRetrains    = 5
	retrainSkipPct = 0.92 // don't restart a transfer that's almost done
)

type speedSample struct {
	t time.Time
	n int64
}

type speedWatch struct {
	mu         sync.Mutex
	start      time.Time
	cum        int64
	total      int64
	peak       float64
	samples    []speedSample
	belowSince time.Time
}

func newSpeedWatch(total int64) *speedWatch {
	return &speedWatch{start: time.Now(), total: total}
}

func (w *speedWatch) reset() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.start = time.Now()
	w.cum = 0
	w.peak = 0
	w.samples = w.samples[:0]
	w.belowSince = time.Time{}
}

func (w *speedWatch) add(n int, total int64) bool {
	if w == nil || n <= 0 {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if total > 0 {
		w.total = total
	}
	now := time.Now()
	w.cum += int64(n)
	w.samples = append(w.samples, speedSample{t: now, n: w.cum})
	cut := now.Add(-stallWindow * 3)
	i := 0
	for i < len(w.samples) && w.samples[i].t.Before(cut) {
		i++
	}
	if i > 0 {
		w.samples = append(w.samples[:0], w.samples[i:]...)
	}

	if now.Sub(w.start) < stallWarmup {
		return false
	}
	if w.total > 0 && float64(w.cum) >= retrainSkipPct*float64(w.total) {
		return false
	}

	bps := w.windowBps(now)
	if bps <= 0 {
		if w.belowSince.IsZero() {
			w.belowSince = now
		}
		return !w.belowSince.IsZero() && now.Sub(w.belowSince) >= stallHold && w.peak >= stallMinPeak
	}
	if bps > w.peak {
		w.peak = bps
	}
	if w.peak < stallMinPeak {
		w.belowSince = time.Time{}
		return false
	}
	if bps < stallRatio*w.peak {
		if w.belowSince.IsZero() {
			w.belowSince = now
		}
		return now.Sub(w.belowSince) >= stallHold
	}
	w.belowSince = time.Time{}
	return false
}

func (w *speedWatch) windowBps(now time.Time) float64 {
	if len(w.samples) < 2 {
		return 0
	}
	oldest := w.samples[0]
	for _, s := range w.samples {
		if now.Sub(s.t) <= stallWindow {
			oldest = s
			break
		}
		oldest = s
	}
	dt := now.Sub(oldest.t).Seconds()
	if dt < 0.4 {
		return 0
	}
	dn := float64(w.cum - oldest.n)
	if dn < 0 {
		return 0
	}
	return dn / dt
}

type watchedConn struct {
	net.Conn
	watch *speedWatch
	total int64
}

func (c *watchedConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	n, err := c.Conn.Read(p)
	if n > 0 && c.watch.add(n, c.total) {
		if err == nil {
			err = errStall
		}
	}
	return n, err
}

func (c *watchedConn) Write(p []byte) (int, error) {
	_ = c.Conn.SetWriteDeadline(time.Now().Add(45 * time.Second))
	n, err := c.Conn.Write(p)
	if n > 0 && c.watch.add(n, c.total) {
		if err == nil {
			err = errStall
		}
	}
	return n, err
}

func wrapWatched(conn net.Conn, total int64) (*watchedConn, *speedWatch) {
	w := newSpeedWatch(total)
	return &watchedConn{Conn: conn, watch: w, total: total}, w
}

func dialTuned(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, err
	}
	setTCPOptions(conn)
	return conn, nil
}

func setTCPOptions(conn net.Conn) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetReadBuffer(tcpBufferSize)
	_ = tc.SetWriteBuffer(tcpBufferSize)
	_ = tc.SetNoDelay(true)
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(30 * time.Second)
}

func closeRetrain(conn net.Conn) {
	if conn == nil {
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}
