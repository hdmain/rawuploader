package main

import (
	"io"
	"testing"
	"time"
)

func TestUDPStreamRoundTrip(t *testing.T) {
	ln, err := ListenUDPStream("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, err := io.ReadFull(c, buf[:11])
		if err != nil {
			done <- err
			return
		}
		if string(buf[:n]) != "hello-world" {
			done <- errPacketLoss
			return
		}
		_, err = c.Write([]byte("ack-ok"))
		done <- err
	}()

	client, err := DialUDPStream(addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("hello-world")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	n, err := io.ReadFull(client, buf[:6])
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ack-ok" {
		t.Fatalf("got %q", buf[:n])
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server side timeout")
	}
}
