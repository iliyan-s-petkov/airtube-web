// Package ratelimit implements the origin-side token buckets and the
// enumeration-breadth counters from Phase 1 §8.2 and §8.3.
//
// The edge (Cloudflare) is the first line and absorbs volumetric floods. These
// buckets are the second: they still work when the edge is bypassed or a rule is
// mis-scoped, and they are the only control that sees the shape of a request —
// which area, which sensor — rather than just its rate.
package ratelimit

import (
	"context"
	"hash/maphash"
	"math"
	"sync"
	"time"

	"airbg.org/internal/config"
)

// Rate is a refill rate and a maximum burst, both in tokens.
type Rate struct {
	PerSecond float64
	Burst     float64
}

type bucket struct {
	tokens   float64
	lastFill time.Time
	lastSeen time.Time
}

// shardCount used to be a fixed power of two here; it is now cfg.ShardCount
// (config.RateLimit.ShardCount), shared by every bucket New creates. Shards
// reduce lock contention only — a single mutex would serialise every request
// in the process on one lock, which turns the rate limiter into the
// throughput ceiling, the opposite of what it is for.
type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newShard() *shard {
	return &shard{buckets: make(map[string]*bucket)}
}

type Limiter struct {
	rate Rate
	ttl  time.Duration

	shards []*shard
	seed   maphash.Seed

	// now is swappable so tests drive time explicitly. A rate limiter tested
	// against the wall clock either sleeps or flakes.
	nowMu sync.RWMutex
	now   func() time.Time
}

// New builds a Limiter from cfg, sharded across shardCount shards.
//
// shardCount is a separate argument rather than a field on config.Bucket:
// it is shared by both the API and series buckets and lives one level up, on
// config.RateLimit.ShardCount. Copying it into config.Bucket would let the two
// copies quietly diverge, which is exactly what this phase exists to remove.
func New(cfg config.Bucket, shardCount int) *Limiter {
	shards := make([]*shard, shardCount)
	for i := range shards {
		shards[i] = newShard()
	}
	return &Limiter{
		rate:   Rate{PerSecond: cfg.PerSecond, Burst: cfg.Burst},
		ttl:    cfg.TTL,
		shards: shards,
		seed:   maphash.MakeSeed(),
		now:    time.Now,
	}
}

func (l *Limiter) SetClockForTesting(now func() time.Time) {
	l.nowMu.Lock()
	defer l.nowMu.Unlock()
	l.now = now
}

func (l *Limiter) clock() time.Time {
	l.nowMu.RLock()
	defer l.nowMu.RUnlock()
	return l.now()
}

func (l *Limiter) shardFor(key string) *shard {
	// A modulo, not a power-of-two mask: shardCount now comes from
	// config.RateLimit.ShardCount (and can be overridden by AIRBG_RATE_LIMIT_SHARD_COUNT),
	// so it is no longer guaranteed to be a power of two the way the old
	// hardcoded 32 was.
	h := maphash.String(l.seed, key)
	return l.shards[h%uint64(len(l.shards))]
}

// Allow spends one token for key.
//
// When denied it returns how long until the next token, so the handler can send
// a truthful Retry-After. A 429 with no Retry-After — or a guessed one — pushes
// well-behaved clients into blind retry loops, which adds load precisely when
// the origin is already refusing work.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.clock()
	sh := l.shardFor(key)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		b = &bucket{tokens: l.rate.Burst, lastFill: now}
		sh.buckets[key] = b
	}

	// Refill for the elapsed time, saturating at Burst. The cap is the point:
	// without it an idle client accumulates unbounded credit and can discharge
	// an arbitrarily large spike, which averages out on a graph while still
	// overwhelming the origin in the moment.
	if elapsed := now.Sub(b.lastFill); elapsed > 0 {
		b.tokens = math.Min(l.rate.Burst, b.tokens+elapsed.Seconds()*l.rate.PerSecond)
		b.lastFill = now
	}

	// lastSeen is updated on every call, allowed or not. Tracking only allowed
	// requests would let a client being actively throttled fall idle by the
	// eviction sweep's reckoning, get its bucket dropped, and come back with a
	// full one.
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	if l.rate.PerSecond <= 0 {
		// No refill configured: nothing will ever free a token. Report the TTL
		// rather than an infinite or zero wait.
		return false, l.ttl
	}
	need := 1 - b.tokens
	return false, time.Duration(need / l.rate.PerSecond * float64(time.Second)).Round(time.Second)
}

// Evict drops buckets untouched for longer than the TTL.
func (l *Limiter) Evict() {
	cutoff := l.clock().Add(-l.ttl)
	for _, sh := range l.shards {
		sh.mu.Lock()
		for k, b := range sh.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(sh.buckets, k)
			}
		}
		sh.mu.Unlock()
	}
}

func (l *Limiter) Len() int {
	n := 0
	for _, sh := range l.shards {
		sh.mu.Lock()
		n += len(sh.buckets)
		sh.mu.Unlock()
	}
	return n
}

// ShardLensForTesting exposes the bucket count of each shard so a test can
// tell "cfg.ShardCount actually shards traffic" apart from "cfg.ShardCount is
// stored but every key funnels into one shard" — both look identical through
// Len(), which only ever reports the sum.
func (l *Limiter) ShardLensForTesting() []int {
	lens := make([]int, len(l.shards))
	for i, sh := range l.shards {
		sh.mu.Lock()
		lens[i] = len(sh.buckets)
		sh.mu.Unlock()
	}
	return lens
}

// StartEvicting runs Evict on a ticker until ctx is cancelled.
func (l *Limiter) StartEvicting(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.Evict()
			}
		}
	}()
}
