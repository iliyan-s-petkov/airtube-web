// Package admit bounds how many requests may be in a section of code at once.
//
// This is admission control, and it is a different control from rate limiting
// and from the connection-pool bulkhead. A rate limiter bounds ONE client. The
// bulkhead (db.OpenPair) stops two workloads from consuming each other's
// capacity. Neither bounds THE CROWD: N well-behaved clients, each within its
// own limit, can still collectively queue far more concurrent database work than
// the pool can serve, and the excess waits inside pgxpool.Acquire holding a
// goroutine and a socket until the write timeout fires. That is queue collapse,
// and every per-client control correctly reports a healthy system while it
// happens.
//
// The refusal is therefore immediate and never queued. Failing a fraction of
// requests in microseconds keeps the site working; queueing all of them for 30
// seconds is an outage.
package admit

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Semaphore is a counted, non-blocking gate. Safe for concurrent use.
type Semaphore struct {
	slots    chan struct{}
	inFlight atomic.Int64
}

// New returns a semaphore admitting size concurrent holders.
//
// size must be positive: a zero-sized semaphore refuses every request, which is
// a total outage dressed as a capacity setting.
func New(size int) (*Semaphore, error) {
	if size < 1 {
		return nil, fmt.Errorf("admit: size must be at least 1, got %d", size)
	}
	return &Semaphore{slots: make(chan struct{}, size)}, nil
}

// TryAcquire takes a slot if one is free. ok reports whether it did; release
// must be called exactly when ok is true, and is safe to call more than once.
//
// The returned closure is idempotent because handlers have early returns, and a
// double release would credit a slot that was never held — letting the
// semaphore admit more than size holders. A cap that silently stops capping is
// worse than no cap, because nothing looks wrong.
func (s *Semaphore) TryAcquire() (release func(), ok bool) {
	select {
	case s.slots <- struct{}{}:
		s.inFlight.Add(1)
		var once sync.Once
		return func() {
			once.Do(func() {
				s.inFlight.Add(-1)
				<-s.slots
			})
		}, true
	default:
		return nil, false
	}
}

// InFlight reports the current holder count, for metrics and tests.
func (s *Semaphore) InFlight() int { return int(s.inFlight.Load()) }
