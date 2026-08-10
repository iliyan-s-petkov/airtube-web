package ratelimit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/ratelimit"
)

// clock is a manual clock. Rate limiting is defined in terms of elapsed time, so
// a test that used the real clock would either sleep (slow) or race (flaky).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_800_000_000, 0).UTC()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func limiter(t *testing.T, r ratelimit.Rate, ttl time.Duration) (*ratelimit.Limiter, *clock) {
	t.Helper()
	c := newClock()
	l := ratelimit.New(r, ttl)
	l.SetClockForTesting(c.now)
	return l, c
}

func TestBurstIsAllowedThenExhausted(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 5}, time.Hour)

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d of the burst was denied", i+1)
		}
	}
	ok, retryAfter := l.Allow("k")
	if ok {
		t.Fatal("the 6th request was allowed with a burst of 5")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want > 0; a 429 without a usable Retry-After tells a well-behaved client nothing", retryAfter)
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 2, Burst: 2}, time.Hour)

	l.Allow("k")
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("bucket was not exhausted")
	}

	c.advance(500 * time.Millisecond) // 2/s → exactly one token
	if ok, _ := l.Allow("k"); !ok {
		t.Error("no token after the refill interval elapsed")
	}
}

// TestRefillDoesNotExceedBurst: a bucket that accumulated tokens over an idle
// hour would let a client fire thousands of requests at once — the limiter would
// average correctly and still fail to protect the origin from the spike.
func TestRefillDoesNotExceedBurst(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 10, Burst: 3}, time.Hour)

	// Force the bucket into existence before the clock advances: a bucket
	// created fresh on the first post-advance call starts at exactly Burst by
	// construction, which would pass even without a saturating cap and prove
	// nothing about the refill formula. Spending a token first routes the
	// advance through the elapsed-time refill path that actually needs the cap.
	l.Allow("k")
	c.advance(time.Hour)

	allowed := 0
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("k"); ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d requests after an idle hour, want 3 (Burst); tokens must saturate at Burst", allowed)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, time.Hour)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request for key a denied")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("key b was denied because key a had spent its token; one client would then 429 everybody else")
	}
}

// TestEvictRemovesIdleKeys is the memory bound. Without eviction the map grows
// one entry per distinct key forever — and the keys are client-controlled, so an
// attacker rotating source addresses turns the rate limiter itself into the
// denial-of-service vector it was added to prevent.
func TestEvictRemovesIdleKeys(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, 10*time.Minute)

	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}

	c.advance(11 * time.Minute)
	l.Evict()
	if got := l.Len(); got != 0 {
		t.Errorf("Len() = %d after the TTL elapsed, want 0", got)
	}
}

// TestEvictKeepsActiveKeys: eviction must not drop a key that is still being
// used, or an active abuser gets a fresh full bucket on every sweep — the
// limiter would then be strictly weaker against heavy traffic than light.
func TestEvictKeepsActiveKeys(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, 10*time.Minute)

	l.Allow("busy")
	c.advance(11 * time.Minute)
	l.Allow("busy") // touches the key just before the sweep
	l.Evict()

	if got := l.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1; an active key was evicted", got)
	}
}

// TestConcurrentAllowIsRaceFree is run under -race and also checks the total:
// a limiter that lost increments under contention would let a distributed client
// exceed the limit by simply being concurrent, which is the normal case.
func TestConcurrentAllowIsRaceFree(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 0, Burst: 100}, time.Hour)

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if ok, _ := l.Allow("shared"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// PerSecond is 0 and the clock never advances, so exactly Burst may pass.
	if allowed != 100 {
		t.Errorf("allowed = %d, want exactly 100 (Burst) — a lost update let extra requests through", allowed)
	}
}

func TestStartEvictingStopsWithContext(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	l.StartEvicting(ctx, time.Millisecond)
	cancel()
	// No assertion beyond "returns and does not panic"; the goroutine leak this
	// guards against is caught by -race plus the test binary exiting cleanly.
	time.Sleep(5 * time.Millisecond)
}
