package httpx

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestLimitListenerClosesConnectionsOverTheCap.
//
// Over-cap connections are CLOSED, not queued. Queuing them is what the kernel
// backlog already does; the point of this listener is that a connection past the
// cap costs the process no file descriptor and no goroutine. The client sees an
// immediate EOF, which is the honest signal.
func TestLimitListenerClosesConnectionsOverTheCap(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := LimitListener(base, 1)
	t.Cleanup(func() { ln.Close() })

	// Accept in the background: Accept must be called for the limiter to see
	// connections at all.
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	first, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { first.Close() })

	// Bounded, like the equivalent wait below: an unbounded receive here would
	// hang the whole suite to the 10-minute panic instead of failing with a
	// useful message if the limiter admits nothing.
	var held net.Conn
	select {
	case held = <-accepted: // the one permitted connection, still open
	case <-time.After(2 * time.Second):
		t.Fatal("the one permitted connection was never accepted")
	}

	second, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	// The over-cap connection must be closed by the server without ever being
	// handed to Accept.
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := second.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read on the over-cap connection = %v, want io.EOF — it was not closed", err)
	}

	// And releasing the first must free the slot.
	held.Close()
	third, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	t.Cleanup(func() { third.Close() })
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Error("a connection after the first was closed was not accepted; the slot was not released")
	}
}

// TestLimitListenerCountsRejections. A cap that sheds silently is a cap nobody
// can size: an operator needs to see this number climbing before they hear about
// it from users.
func TestLimitListenerCountsRejections(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := LimitListener(base, 1)
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()

	before := ConnectionsRejectedCountForTesting()

	first, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = second.Read(make([]byte, 1))

	// Delta, not absolute: the counter is process-global.
	if got := ConnectionsRejectedCountForTesting() - before; got < 1 {
		t.Errorf("rejections = %d, want at least 1", got)
	}
}

// TestLimitListenerDoubleCloseDoesNotHang. net/http can call Close more than
// once on the same connection. Without the sync.Once guard, a second Close
// does <-l.slots on a channel the first Close already drained, so the receive
// blocks forever — the guard is not about avoiding an over-credit, it is about
// avoiding a caller (a net/http connection goroutine, in production) that hangs
// forever on its own second Close.
func TestLimitListenerDoubleCloseDoesNotHang(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := LimitListener(base, 1)
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	dialed, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { dialed.Close() })

	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was never accepted")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	done := make(chan struct{})
	go func() {
		conn.Close() // second Close on the same connection
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("second Close did not return within 1s; it is blocked on an already-drained slots channel")
	}
}

// TestLimitListenerWithNonPositiveMaxIsUnlimited. A zero cap must not mean "no
// connections": a mis-set variable that silently refuses every visitor is a
// worse outcome than one that silently declines to limit, and config already
// rejects a non-positive value before this is reached.
func TestLimitListenerWithNonPositiveMaxIsUnlimited(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if got := LimitListener(base, 0); got != base {
		t.Error("LimitListener(ln, 0) wrapped the listener; a non-positive cap must be a no-op")
	}
	base.Close()
}
