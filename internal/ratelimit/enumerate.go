package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Limits from Phase 1 §8.3.
//
// 12 distinct areas per hour: Bulgaria has 28 oblasti and 28 cities. A curious
// visitor compares their own city with a handful of others; nobody legitimately
// opens half the country in an hour. 40 distinct sensors: a dense city page
// shows well under that, and a user clicking individual markers gets bored long
// before reaching it.
//
// Both are breadth, not volume, on purpose: volume limits punish enthusiasm and
// miss a patient scraper pacing itself under the rate limit.
const (
	DistinctAreaLimit   = 12
	DistinctSensorLimit = 40
	EnumerationWindow   = time.Hour
)

type breadthEntry struct {
	windowStart time.Time
	lastSeen    time.Time
	slugs       map[string]struct{}
	sensors     map[string]struct{}
	// tripped is sticky for the rest of the window. Without it a client that
	// walked the country could replay its visited set freely after tripping.
	areaTripped   bool
	sensorTripped bool
}

// Breadth counts how many distinct areas and sensors each client key touches
// within a rolling window.
type Breadth struct {
	areaLimit   int
	sensorLimit int
	window      time.Duration

	mu      sync.Mutex
	entries map[string]*breadthEntry

	nowMu sync.RWMutex
	now   func() time.Time
}

func NewBreadth(areaLimit, sensorLimit int, window time.Duration) *Breadth {
	return &Breadth{
		areaLimit: areaLimit, sensorLimit: sensorLimit, window: window,
		entries: make(map[string]*breadthEntry), now: time.Now,
	}
}

func (b *Breadth) SetClockForTesting(now func() time.Time) {
	b.nowMu.Lock()
	defer b.nowMu.Unlock()
	b.now = now
}

func (b *Breadth) clock() time.Time {
	b.nowMu.RLock()
	defer b.nowMu.RUnlock()
	return b.now()
}

// ObserveArea records that key requested slug and reports whether to serve it.
//
// A single mutex here rather than the sharded design the token buckets use:
// this is called once per area request, not per request, and it mutates two maps
// per call — sharding it would add complexity for a lock that is not hot.
func (b *Breadth) ObserveArea(key, slug string) bool {
	return b.observe(key, slug, false)
}

func (b *Breadth) ObserveSensor(key string, sensorID int64) bool {
	return b.observe(key, strconv.FormatInt(sensorID, 10), true)
}

func (b *Breadth) observe(key, value string, sensor bool) bool {
	now := b.clock()

	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		e = &breadthEntry{
			windowStart: now,
			slugs:       make(map[string]struct{}),
			sensors:     make(map[string]struct{}),
		}
		b.entries[key] = e
	}

	// Roll the window. A fixed window rather than a sliding one: sliding would
	// need per-observation timestamps, and the worst case a fixed window allows
	// — 2×limit across a boundary — is well inside the tolerance of a check
	// whose limits are already set generously to avoid false positives.
	if now.Sub(e.windowStart) >= b.window {
		e.windowStart = now
		e.slugs = make(map[string]struct{})
		e.sensors = make(map[string]struct{})
		e.areaTripped = false
		e.sensorTripped = false
	}
	e.lastSeen = now

	set, limit, tripped := e.slugs, b.areaLimit, &e.areaTripped
	if sensor {
		set, limit, tripped = e.sensors, b.sensorLimit, &e.sensorTripped
	}

	if *tripped {
		// Already over: refuse without recording. Recording would let a client
		// we have already refused keep growing our memory.
		return false
	}

	if _, seen := set[value]; seen {
		// Revisiting something already counted is free. This is what makes
		// "reads one city's page all day" indistinguishable from one request.
		return true
	}

	if len(set) >= limit {
		*tripped = true
		return false
	}
	set[value] = struct{}{}
	return true
}

func (b *Breadth) Evict() {
	// Entries are dropped after two windows of silence rather than one, so a
	// client whose window is still open is not handed a clean slate early.
	cutoff := b.clock().Add(-2 * b.window)

	b.mu.Lock()
	defer b.mu.Unlock()
	for k, e := range b.entries {
		if e.lastSeen.Before(cutoff) {
			delete(b.entries, k)
		}
	}
}

func (b *Breadth) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// SlugSetSizeForTesting exposes one entry's slug-set size so a test can assert
// that a tripped client stops consuming memory.
func (b *Breadth) SlugSetSizeForTesting(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return 0
	}
	return len(e.slugs)
}

func (b *Breadth) StartEvicting(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.Evict()
			}
		}
	}()
}
