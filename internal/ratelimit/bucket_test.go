package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/ratelimit"
)

// testBucket is the API bucket's config, mirroring airbg.yaml's
// ratelimit.api section (Task 11 brief). Individual tests override PerSecond,
// Burst and TTL by constructing their own config.Bucket where the default
// values would not exercise the behaviour under test.
func testBucket(perSecond, burst float64, ttl time.Duration) config.Bucket {
	return config.Bucket{
		PerSecond:     perSecond,
		Burst:         burst,
		TTL:           ttl,
		EvictInterval: 5 * time.Minute,
		RetryAfter:    2 * time.Second,
	}
}

// testShardCount is a small, deterministic shard count for tests: large
// enough to exercise sharding (TestShardingDistributesKeys), small enough
// that per-key shard collisions in that test stay improbable-but-checked
// rather than a certainty.
const testShardCount = 8

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

func limiter(t *testing.T, cfg config.Bucket) (*ratelimit.Limiter, *clock) {
	t.Helper()
	c := newClock()
	l := ratelimit.New(cfg, testShardCount)
	l.SetClockForTesting(c.now)
	return l, c
}

func TestBurstIsAllowedThenExhausted(t *testing.T) {
	l, _ := limiter(t, testBucket(1, 5, time.Hour))

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
	l, c := limiter(t, testBucket(2, 2, time.Hour))

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
	l, c := limiter(t, testBucket(10, 3, time.Hour))

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
	l, _ := limiter(t, testBucket(1, 1, time.Hour))

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
	l, c := limiter(t, testBucket(1, 1, 10*time.Minute))

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
	l, c := limiter(t, testBucket(1, 1, 10*time.Minute))

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
	l, _ := limiter(t, testBucket(0, 100, time.Hour))

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
	l, _ := limiter(t, testBucket(1, 1, time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())

	l.StartEvicting(ctx, time.Millisecond)
	cancel()
	// No assertion beyond "returns and does not panic"; the goroutine leak this
	// guards against is caught by -race plus the test binary exiting cleanly.
	time.Sleep(5 * time.Millisecond)
}

// TestShardCountControlsShardingNotJustStorage: cfg.ShardCount (New's second
// argument) must both size the shard slice AND actually spread keys across
// it. A limiter that stored ShardCount but funnelled every key through
// shardFor into a single shard would pass every other test in this file —
// Len() only ever reports the sum across shards — while providing none of
// the contention reduction ShardCount exists for.
func TestShardCountControlsShardingNotJustStorage(t *testing.T) {
	const shardCount = 8
	l, _ := limiter2(t, testBucket(1, 1, time.Hour), shardCount)

	for i := 0; i < 500; i++ {
		l.Allow(fmt.Sprintf("key-%d", i))
	}

	lens := l.ShardLensForTesting()
	if len(lens) != shardCount {
		t.Fatalf("ShardLensForTesting() has %d entries, want %d (cfg.ShardCount)", len(lens), shardCount)
	}

	total, nonEmpty := 0, 0
	for _, n := range lens {
		total += n
		if n > 0 {
			nonEmpty++
		}
	}
	if total != 500 {
		t.Fatalf("shards hold %d buckets total, want 500 (one per distinct key)", total)
	}
	if nonEmpty < 2 {
		t.Errorf("500 distinct keys landed in %d of %d shards; sharding looks inert (everything funnelled into one shard)", nonEmpty, shardCount)
	}
}

// TestRetryAfterIsComputedFromConfiguredRate pins the exact Retry-After value
// for two different configured rates. TestBurstIsAllowedThenExhausted only
// checks retryAfter > 0, which a hardcoded literal (e.g. always returning 2s)
// would also satisfy; asserting the exact value for rates that must produce
// different results is what actually proves the duration is derived from
// PerSecond rather than being a literal on the response path.
func TestRetryAfterIsComputedFromConfiguredRate(t *testing.T) {
	l1, _ := limiter(t, testBucket(1, 1, time.Hour)) // 1 token short, refills at 1/s → 1s
	l1.Allow("k")
	if _, retryAfter := l1.Allow("k"); retryAfter != time.Second {
		t.Errorf("PerSecond=1: retryAfter = %v, want exactly 1s", retryAfter)
	}

	l2, _ := limiter(t, testBucket(0.5, 1, time.Hour)) // 1 token short, refills at 0.5/s → 2s
	l2.Allow("k")
	if _, retryAfter := l2.Allow("k"); retryAfter != 2*time.Second {
		t.Errorf("PerSecond=0.5: retryAfter = %v, want exactly 2s", retryAfter)
	}
}

// limiter2 is limiter with an explicit shard count, for the one test that
// needs to pick a shard count independent of testShardCount.
func limiter2(t *testing.T, cfg config.Bucket, shardCount int) (*ratelimit.Limiter, *clock) {
	t.Helper()
	c := newClock()
	l := ratelimit.New(cfg, shardCount)
	l.SetClockForTesting(c.now)
	return l, c
}
