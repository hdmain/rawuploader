package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	udpMagic      = "tcpU"
	udpHeaderLen  = 15 // magic(4)+type(1)+session(4)+seq(4)+len(2)
	udpMaxPayload = 1200

	udpTypeSYN    = 1
	udpTypeSYNACK = 2
	udpTypeDATA   = 3
	udpTypeFIN    = 4
	udpTypeRST    = 5

	udpDialTimeout     = 30 * time.Second
	udpGapTimeout      = 3 * time.Second
	udpHandshakeWait   = 8 * time.Second
	udpPaceBytesPerSec = 80 * 1024 * 1024
)

var (
	errPacketLoss   = errors.New("udp packet loss")
	errUDPHandshake = errors.New("udp handshake failed")
	errUDPClosed    = errors.New("udp stream closed")
)

type udpPacket struct {
	typ     byte
	session uint32
	seq     uint32
	payload []byte
	isFIN   bool
}

func encodeUDPPacket(typ byte, session, seq uint32, payload []byte) []byte {
	if len(payload) > udpMaxPayload {
		payload = payload[:udpMaxPayload]
	}
	buf := make([]byte, udpHeaderLen+len(payload))
	copy(buf[0:4], udpMagic)
	buf[4] = typ
	binary.BigEndian.PutUint32(buf[5:9], session)
	binary.BigEndian.PutUint32(buf[9:13], seq)
	binary.BigEndian.PutUint16(buf[13:15], uint16(len(payload)))
	copy(buf[15:], payload)
	return buf
}

func decodeUDPPacket(b []byte) (udpPacket, error) {
	if len(b) < udpHeaderLen {
		return udpPacket{}, errors.New("short udp packet")
	}
	if string(b[0:4]) != udpMagic {
		return udpPacket{}, errors.New("bad udp magic")
	}
	plen := int(binary.BigEndian.Uint16(b[13:15]))
	if udpHeaderLen+plen > len(b) {
		return udpPacket{}, errors.New("truncated udp payload")
	}
	payload := make([]byte, plen)
	copy(payload, b[udpHeaderLen:udpHeaderLen+plen])
	typ := b[4]
	return udpPacket{
		typ:     typ,
		session: binary.BigEndian.Uint32(b[5:9]),
		seq:     binary.BigEndian.Uint32(b[9:13]),
		payload: payload,
		isFIN:   typ == udpTypeFIN,
	}, nil
}

type sessionKey struct {
	addr    string
	session uint32
}

// UDPListener accepts raw-UDP stream sessions (no retransmission).
type UDPListener struct {
	pc       net.PacketConn
	acceptCh chan *UDPConn
	mu       sync.Mutex
	sessions map[sessionKey]*UDPConn
	closed   bool
	done     chan struct{}
}

func ListenUDPStream(network, address string) (*UDPListener, error) {
	pc, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	ln := &UDPListener{
		pc:       pc,
		acceptCh: make(chan *UDPConn, 64),
		sessions: make(map[sessionKey]*UDPConn),
		done:     make(chan struct{}),
	}
	go ln.loop()
	return ln, nil
}

func (ln *UDPListener) Addr() net.Addr { return ln.pc.LocalAddr() }

func (ln *UDPListener) Close() error {
	ln.mu.Lock()
	if ln.closed {
		ln.mu.Unlock()
		return nil
	}
	ln.closed = true
	close(ln.done)
	sessions := make([]*UDPConn, 0, len(ln.sessions))
	for _, c := range ln.sessions {
		sessions = append(sessions, c)
	}
	ln.sessions = make(map[sessionKey]*UDPConn)
	ln.mu.Unlock()
	for _, c := range sessions {
		c.forceClose()
	}
	return ln.pc.Close()
}

func (ln *UDPListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-ln.acceptCh:
		if !ok || c == nil {
			return nil, errUDPClosed
		}
		return c, nil
	case <-ln.done:
		return nil, errUDPClosed
	}
}

func (ln *UDPListener) SetDeadline(t time.Time) error {
	return ln.pc.SetDeadline(t)
}

func (ln *UDPListener) removeSession(key sessionKey) {
	ln.mu.Lock()
	delete(ln.sessions, key)
	ln.mu.Unlock()
}

func (ln *UDPListener) loop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := ln.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-ln.done:
				return
			default:
				continue
			}
		}
		pkt, err := decodeUDPPacket(buf[:n])
		if err != nil {
			continue
		}
		key := sessionKey{addr: addr.String(), session: pkt.session}

		ln.mu.Lock()
		if ln.closed {
			ln.mu.Unlock()
			return
		}
		conn := ln.sessions[key]
		switch pkt.typ {
		case udpTypeSYN:
			if conn == nil {
				conn = newUDPConn(ln.pc, ln.pc.LocalAddr(), addr, pkt.session, false, func() {
					ln.removeSession(key)
				})
				ln.sessions[key] = conn
				_ = conn.sendCtrl(udpTypeSYNACK, 0, nil)
				select {
				case ln.acceptCh <- conn:
				default:
					conn.forceClose()
					delete(ln.sessions, key)
				}
			} else {
				_ = conn.sendCtrl(udpTypeSYNACK, 0, nil)
			}
			ln.mu.Unlock()
		case udpTypeDATA, udpTypeFIN, udpTypeRST:
			ln.mu.Unlock()
			if conn != nil {
				conn.ingest(pkt)
			}
		default:
			ln.mu.Unlock()
		}
	}
}

// UDPConn is a net.Conn over raw UDP with sequencing but no retransmission.
type UDPConn struct {
	pc         net.PacketConn
	local      net.Addr
	remote     net.Addr
	session    uint32
	ownsPC     bool
	onClose    func()

	mu       sync.Mutex
	closed   bool
	readErr  error
	writeErr error
	nextSend uint32
	nextRecv uint32
	oob      map[uint32]udpPacket
	inbuf    []byte
	readCond *sync.Cond
	finRecv  bool

	readDeadline  time.Time
	writeDeadline time.Time
	lastPace      time.Time
}

func newUDPConn(pc net.PacketConn, local, remote net.Addr, session uint32, _ bool, onClose func()) *UDPConn {
	c := &UDPConn{
		pc:       pc,
		local:    local,
		remote:   remote,
		session:  session,
		onClose:  onClose,
		oob:      make(map[uint32]udpPacket),
		nextSend: 1,
		nextRecv: 1,
	}
	c.readCond = sync.NewCond(&c.mu)
	return c
}

func DialUDPStream(addr string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = udpDialTimeout
	}
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return nil, err
	}
	session := uint32(time.Now().UnixNano() & 0xffffffff)
	if session == 0 {
		session = 1
	}
	c := newUDPConn(pc, pc.LocalAddr(), raddr, session, true, nil)
	c.ownsPC = true

	deadline := time.Now().Add(timeout)
	if err := c.sendCtrl(udpTypeSYN, 0, nil); err != nil {
		c.forceClose()
		return nil, err
	}

	buf := make([]byte, 2048)
	for {
		if time.Now().After(deadline) {
			c.forceClose()
			return nil, errUDPHandshake
		}
		_ = pc.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				_ = c.sendCtrl(udpTypeSYN, 0, nil)
				continue
			}
			c.forceClose()
			return nil, err
		}
		pkt, err := decodeUDPPacket(buf[:n])
		if err != nil || pkt.session != session {
			continue
		}
		if pkt.typ == udpTypeSYNACK {
			c.remote = from
			_ = pc.SetDeadline(time.Time{})
			go c.clientReadLoop()
			return c, nil
		}
		if pkt.typ == udpTypeRST {
			c.forceClose()
			return nil, errUDPHandshake
		}
	}
}

func (c *UDPConn) clientReadLoop() {
	buf := make([]byte, 2048)
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		_ = c.pc.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, from, err := c.pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.mu.Lock()
			if !c.closed && c.readErr == nil {
				c.readErr = err
				c.readCond.Broadcast()
			}
			c.mu.Unlock()
			return
		}
		c.remote = from
		pkt, err := decodeUDPPacket(buf[:n])
		if err != nil || pkt.session != c.session {
			continue
		}
		c.ingest(pkt)
	}
}

func (c *UDPConn) sendCtrl(typ byte, seq uint32, payload []byte) error {
	pkt := encodeUDPPacket(typ, c.session, seq, payload)
	_, err := c.pc.WriteTo(pkt, c.remote)
	return err
}

func (c *UDPConn) ingest(pkt udpPacket) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	switch pkt.typ {
	case udpTypeRST:
		c.readErr = errUDPClosed
		c.writeErr = errUDPClosed
		c.readCond.Broadcast()
	case udpTypeFIN, udpTypeDATA:
		if pkt.seq < c.nextRecv {
			return
		}
		if pkt.seq > c.nextRecv {
			if len(c.oob) < 4096 {
				c.oob[pkt.seq] = pkt
			}
			return
		}
		// in-order
		c.deliverLocked(pkt)
		for {
			next, ok := c.oob[c.nextRecv]
			if !ok {
				break
			}
			delete(c.oob, c.nextRecv)
			c.deliverLocked(next)
		}
		c.readCond.Broadcast()
	}
}

func (c *UDPConn) deliverLocked(pkt udpPacket) {
	if pkt.typ == udpTypeFIN || pkt.isFIN {
		c.finRecv = true
		c.nextRecv++
		return
	}
	if len(pkt.payload) > 0 {
		c.inbuf = append(c.inbuf, pkt.payload...)
	}
	c.nextRecv++
}

func (c *UDPConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if len(c.inbuf) > 0 {
			n := copy(p, c.inbuf)
			c.inbuf = c.inbuf[n:]
			return n, nil
		}
		if c.finRecv {
			return 0, io.EOF
		}
		if c.readErr != nil {
			return 0, c.readErr
		}
		if c.closed {
			return 0, errUDPClosed
		}
		if !c.readDeadline.IsZero() && time.Now().After(c.readDeadline) {
			return 0, timeoutError{}
		}
		// Wait for data. Only treat as packet loss when out-of-order packets prove a hole.
		wait := time.Hour
		if len(c.oob) > 0 {
			wait = udpGapTimeout
		}
		if !c.readDeadline.IsZero() {
			if d := time.Until(c.readDeadline); d < wait {
				wait = d
			}
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.AfterFunc(wait, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.inbuf) > 0 || c.finRecv || c.readErr != nil {
				c.readCond.Broadcast()
				return
			}
			if len(c.oob) > 0 {
				c.readErr = errPacketLoss
			}
			c.readCond.Broadcast()
		})
		c.readCond.Wait()
		timer.Stop()
	}
}

func (c *UDPConn) pace(n int) {
	if udpPaceBytesPerSec <= 0 || n <= 0 {
		return
	}
	need := time.Duration(float64(n) / float64(udpPaceBytesPerSec) * float64(time.Second))
	if need <= 0 {
		return
	}
	now := time.Now()
	if !c.lastPace.IsZero() {
		elapsed := now.Sub(c.lastPace)
		if elapsed < need {
			time.Sleep(need - elapsed)
		}
	}
	c.lastPace = time.Now()
}

func (c *UDPConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed || c.writeErr != nil {
		err := c.writeErr
		if err == nil {
			err = errUDPClosed
		}
		c.mu.Unlock()
		return 0, err
	}
	if !c.writeDeadline.IsZero() && time.Now().After(c.writeDeadline) {
		c.mu.Unlock()
		return 0, timeoutError{}
	}
	c.mu.Unlock()

	sent := 0
	for sent < len(p) {
		chunk := p[sent:]
		if len(chunk) > udpMaxPayload {
			chunk = chunk[:udpMaxPayload]
		}
		c.mu.Lock()
		if c.closed || c.writeErr != nil {
			err := c.writeErr
			if err == nil {
				err = errUDPClosed
			}
			c.mu.Unlock()
			return sent, err
		}
		seq := c.nextSend
		c.nextSend++
		remote := c.remote
		session := c.session
		c.mu.Unlock()

		pkt := encodeUDPPacket(udpTypeDATA, session, seq, chunk)
		c.pace(len(pkt))
		if _, err := c.pc.WriteTo(pkt, remote); err != nil {
			return sent, err
		}
		sent += len(chunk)
	}
	return sent, nil
}

func (c *UDPConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	seq := c.nextSend
	c.nextSend++
	remote := c.remote
	session := c.session
	c.closed = true
	c.readCond.Broadcast()
	onClose := c.onClose
	owns := c.ownsPC
	pc := c.pc
	c.mu.Unlock()

	_, _ = c.pc.WriteTo(encodeUDPPacket(udpTypeFIN, session, seq, nil), remote)
	if onClose != nil {
		onClose()
	}
	if owns && pc != nil {
		return pc.Close()
	}
	return nil
}

func (c *UDPConn) forceClose() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.readErr = errUDPClosed
	c.writeErr = errUDPClosed
	c.readCond.Broadcast()
	onClose := c.onClose
	owns := c.ownsPC
	pc := c.pc
	c.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	if owns && pc != nil {
		_ = pc.Close()
	}
}

func (c *UDPConn) LocalAddr() net.Addr  { return c.local }
func (c *UDPConn) RemoteAddr() net.Addr { return c.remote }

func (c *UDPConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.readCond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *UDPConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.readCond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *UDPConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "udp stream i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
