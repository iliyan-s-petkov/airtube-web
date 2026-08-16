package httpx

import (
	"net"
	"sync"

	"airbg.org/internal/metrics"
)

// connectionsRejected counts connections closed for exceeding the cap.
//
// Unlabelled: the peer address would be unbounded cardinality handed straight
// to an attacker. Two listeners are capped now — public and tiles — and they
// share this counter, because shedding on either is the same operational event:
// the process is at its socket ceiling. A listener label would be bounded and
// safe to add if an operator ever needs to tell the two apart.
var connectionsRejected = metrics.Counter(
	"airbg_connections_rejected_total",
	"Connections closed immediately because the concurrent-connection cap was reached.")

// LimitListener bounds how many connections may be open at once.
//
// The gap this closes: the server's timeouts bound how long one request may
// take, and nothing bounds how many sockets the process may hold. Tens of
// thousands of mostly-idle connections, each dribbling one header byte every few
// seconds, exhaust file descriptors while completing no request — so no rate
// limiter, breadth counter or admission cap ever sees them.
//
// An over-cap connection is accepted from the kernel and closed immediately,
// rather than left in the backlog. Leaving it queued would make the process look
// healthy while clients hung; closing it is the honest signal, and the
// descriptor is released in the same breath.
//
// A non-positive max returns ln unchanged. A cap that is accidentally zero must
// degrade to "no limiting", never to "no service".
//
// Hand-rolled rather than golang.org/x/net/netutil: the project takes no new Go
// dependency, and this is the whole of what netutil.LimitListener does.
func LimitListener(ln net.Listener, max int) net.Listener {
	if max < 1 {
		return ln
	}
	return &limitListener{Listener: ln, slots: make(chan struct{}, max)}
}

type limitListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			// Counted before the close, so a shed connection is never invisible
			// to metrics even if the close itself errors.
			connectionsRejected.Inc()
			c.Close()
			// Keep accepting: returning an error here would take down the
			// server's Accept loop, turning a shed connection into an outage.
		}
	}
}

// limitConn returns its slot when closed. net/http closes every connection it
// finishes with, including on timeout and on panic recovery, so Close is the
// correct hook — there is no path where the server keeps a connection it has
// stopped serving.
type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	// Once, because a double Close must not release the slot twice: without
	// this guard, a second release does <-l.slots on a buffered channel that is
	// already empty (the first Close drained it), so the second Close blocks
	// forever on that receive — and once a later connection's Close finally
	// sends into the channel, the blocked receive steals that connection's
	// slot instead of its own. The cap shrinks and the caller (a net/http
	// connection goroutine, in production) hangs.
	c.once.Do(c.release)
	return err
}

// ConnectionsRejectedCountForTesting reads the shed counter so a test can assert
// in DELTA. Process-global, so an absolute count would depend on test order.
func ConnectionsRejectedCountForTesting() int64 { return connectionsRejected.Value() }
