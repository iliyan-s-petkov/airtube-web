package ratelimit_test

import (
	"fmt"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/ratelimit"
)

// testEnumerate mirrors airbg.yaml's ratelimit.enumerate section (Task 11
// brief), letting individual tests set their own area/sensor limits while
// keeping the window and retry-after fixed.
func testEnumerate(areaLimit, sensorLimit int) config.Enumerate {
	return config.Enumerate{
		AreasPerWindow:   areaLimit,
		SensorsPerWindow: sensorLimit,
		Window:           time.Hour,
		RetryAfter:       900 * time.Second,
	}
}

func breadth(t *testing.T, areaLimit, sensorLimit int) (*ratelimit.Breadth, *clock) {
	t.Helper()
	c := newClock()
	b := ratelimit.NewBreadth(testEnumerate(areaLimit, sensorLimit))
	b.SetClockForTesting(c.now)
	return b, c
}

// TestRepeatedSameAreaIsNotEnumeration is the false-positive guard, and the
// reason this counts DISTINCT slugs rather than requests. Someone watching one
// city's page all afternoon — a resident checking the air before a run — must
// never be flagged. A volume counter cannot tell them from a scraper; a breadth
// counter can.
func TestRepeatedSameAreaIsNotEnumeration(t *testing.T) {
	b, _ := breadth(t, 3, 10)

	for i := 0; i < 500; i++ {
		if !b.ObserveArea("client", "sofia") {
			t.Fatalf("flagged at request %d for repeatedly viewing ONE area", i+1)
		}
	}
}

// TestEnumerationAreaLimitComesFromConfig pins the actual documented bound —
// 12 distinct areas per hour (airbg.yaml's ratelimit.enumerate.areas_per_window,
// see config's TestResolveCommittedConfig for the resolve-time half of this
// chain) — rather than an arbitrary small number picked for test convenience,
// the way every other test in this file does. NewBreadth taking a hardcoded 12
// instead of cfg.AreasPerWindow would pass every other test here, since they
// all supply their own limit.
func TestEnumerationAreaLimitComesFromConfig(t *testing.T) {
	cfg := config.Enumerate{AreasPerWindow: 12, SensorsPerWindow: 40, Window: time.Hour, RetryAfter: 900 * time.Second}
	c := newClock()
	b := ratelimit.NewBreadth(cfg)
	b.SetClockForTesting(c.now)

	for i := 0; i < 12; i++ {
		slug := fmt.Sprintf("area-%d", i)
		if !b.ObserveArea("client", slug) {
			t.Fatalf("area %d of 12 (the configured limit) was refused", i+1)
		}
	}
	if b.ObserveArea("client", "area-13") {
		t.Error("the 13th distinct area was allowed; ratelimit.enumerate.areas_per_window = 12 was not enforced")
	}
}

func TestDistinctAreasTripTheLimit(t *testing.T) {
	b, _ := breadth(t, 3, 10)

	for _, slug := range []string{"sofia", "plovdiv", "varna"} {
		if !b.ObserveArea("client", slug) {
			t.Fatalf("flagged at or below the limit on %q", slug)
		}
	}
	if b.ObserveArea("client", "burgas") {
		t.Error("the 4th distinct area was allowed with a limit of 3")
	}
}

// TestTrippedClientStaysTrippedForKnownAreas: once over the limit, a client must
// be refused even for a slug it already visited. Otherwise a scraper walks the
// country, trips at the end, and then replays its whole visited set freely —
// which is exactly the extraction the check exists to stop.
func TestTrippedClientStaysTrippedForKnownAreas(t *testing.T) {
	b, _ := breadth(t, 2, 10)

	b.ObserveArea("client", "a")
	b.ObserveArea("client", "b")
	b.ObserveArea("client", "c") // trips

	if b.ObserveArea("client", "a") {
		t.Error("a tripped client was allowed to re-request an already-seen area")
	}
}

func TestDistinctSensorsTripSeparately(t *testing.T) {
	b, _ := breadth(t, 100, 3)

	for _, id := range []int64{1, 2, 3} {
		if !b.ObserveSensor("client", id) {
			t.Fatalf("flagged at or below the sensor limit on %d", id)
		}
	}
	if b.ObserveSensor("client", 4) {
		t.Error("the 4th distinct sensor was allowed with a limit of 3")
	}
	// The area budget must be untouched — the two dimensions are independent,
	// or a sensor-heavy session would lock a user out of the map.
	if !b.ObserveArea("client", "sofia") {
		t.Error("tripping the sensor limit also blocked areas")
	}
}

// The independence checked at the end of TestDistinctSensorsTripSeparately is
// real but cannot fail: that test runs with an area limit of 100, so three
// sensors leaking into the area set still leave 97 of budget and the assertion
// passes either way. Making an observation of one kind consume the other kind's
// budget survived mutation testing for exactly that reason.
//
// The limits here are sized so any leak is fatal rather than absorbed: more
// observations of the first kind than the second kind's limit allows.
//
// The failure this prevents is a lockout of ordinary users, not a hole a
// scraper walks through — click five markers and the map refuses to open an
// oblast — and it is silent, because a breadth counter has no per-request
// symptom until the moment it refuses.
func TestOneKindOfObservationDoesNotSpendTheOtherKindsBudget(t *testing.T) {
	t.Run("sensors do not spend the area budget", func(t *testing.T) {
		b, _ := breadth(t, 3, 10)

		for _, id := range []int64{1, 2, 3, 4, 5} {
			if !b.ObserveSensor("client", id) {
				t.Fatalf("sensor %d refused below the sensor limit of 10", id)
			}
		}
		// Five sensors seen, area limit three: if the sensors were recorded
		// against areas, the first area is already over.
		for _, slug := range []string{"sofia", "plovdiv", "varna"} {
			if !b.ObserveArea("client", slug) {
				t.Errorf("area %q refused within a limit of 3 after 5 sensor views; sensor observations are being counted against the area budget", slug)
			}
		}
	})

	t.Run("areas do not spend the sensor budget", func(t *testing.T) {
		b, _ := breadth(t, 10, 3)

		for _, slug := range []string{"sofia", "plovdiv", "varna", "burgas", "ruse"} {
			if !b.ObserveArea("client", slug) {
				t.Fatalf("area %q refused below the area limit of 10", slug)
			}
		}
		for _, id := range []int64{1, 2, 3} {
			if !b.ObserveSensor("client", id) {
				t.Errorf("sensor %d refused within a limit of 3 after 5 area views; area observations are being counted against the sensor budget", id)
			}
		}
	})
}

func TestKeysAreIndependentForBreadth(t *testing.T) {
	b, _ := breadth(t, 1, 1)

	b.ObserveArea("a", "sofia")
	b.ObserveArea("a", "varna") // a trips

	if !b.ObserveArea("b", "sofia") {
		t.Error("client b was blocked by client a's enumeration; on CGNAT that would lock out a whole mobile network at once")
	}
}

// TestWindowResets: the window must roll, or a client that once tripped is
// blocked forever. On CGNAT one abuser shares an address with thousands of
// legitimate users, so a permanent block is collateral damage measured in
// neighbourhoods.
func TestWindowResets(t *testing.T) {
	b, c := breadth(t, 2, 10)

	b.ObserveArea("client", "a")
	b.ObserveArea("client", "b")
	if b.ObserveArea("client", "c") {
		t.Fatal("did not trip")
	}

	c.advance(61 * time.Minute)
	if !b.ObserveArea("client", "d") {
		t.Error("still blocked after the window elapsed")
	}
}

// TestEvictRemovesIdleBreadthKeys — same unbounded-growth hazard as the token
// buckets, with a bigger footprint: each entry holds two sets, not one counter.
func TestEvictRemovesIdleBreadthKeys(t *testing.T) {
	b, c := breadth(t, 5, 5)

	b.ObserveArea("a", "x")
	b.ObserveArea("b", "y")
	if got := b.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}

	c.advance(3 * time.Hour)
	b.Evict()
	if got := b.Len(); got != 0 {
		t.Errorf("Len() = %d after eviction, want 0", got)
	}
}

// TestEvictKeepsKeysSilentForOneWindow pins the margin Evict documents: entries
// go after TWO windows of silence, not one. Nothing else in this package can
// tell the two apart — TestEvictRemovesIdleBreadthKeys advances three hours, so
// it passes at either setting, and every other test sweeps inside one window.
//
// The margin is what stops a client being handed a clean slate while its own
// window is still open. A key last seen just over one window ago may still have
// a partially-elapsed window's slug set that has to be preserved to mean
// anything.
func TestEvictKeepsKeysSilentForOneWindow(t *testing.T) {
	b, c := breadth(t, 5, 5)

	b.ObserveArea("quiet", "x")
	c.advance(time.Hour + time.Minute) // one window of silence, plus a little
	b.Evict()

	if got := b.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1; a key silent for only one window was evicted", got)
	}
}

// TestSetSizeIsBoundedWhenTripped: after tripping, the sets must stop growing.
// A tripped client that kept inserting every new slug it asked for would let an
// attacker who has ALREADY been flagged keep allocating memory — turning a
// refused request into a slow leak.
func TestSetSizeIsBoundedWhenTripped(t *testing.T) {
	b, _ := breadth(t, 2, 2)

	for i := 0; i < 10_000; i++ {
		b.ObserveArea("client", string(rune(i%0x4000+0x100)))
	}
	if got := b.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 key", got)
	}
	if got := b.SlugSetSizeForTesting("client"); got > 3 {
		t.Errorf("slug set grew to %d entries after tripping; a refused client must stop consuming memory", got)
	}
}

// TestActiveKeyIsNeverEvicted pins that a key touched inside the eviction
// window survives a sweep, so a normal browsing session is never dropped
// mid-window and silently handed a clean slate (or, worse, reset in a way
// that would let the caller re-trip a threshold accidentally).
func TestActiveKeyIsNeverEvicted(t *testing.T) {
	b, c := breadth(t, 5, 5)

	b.ObserveArea("client", "x")
	c.advance(30 * time.Minute)
	b.ObserveArea("client", "y") // keeps it active
	b.Evict()

	if got := b.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 (active key must survive eviction)", got)
	}
}

// TestAreaLimitBoundaryIsExclusive pins the exact threshold semantics at N-1
// and N: the Nth distinct area is allowed, the (N+1)th is refused.
func TestAreaLimitBoundaryIsExclusive(t *testing.T) {
	b, _ := breadth(t, 3, 10)

	if !b.ObserveArea("client", "s1") {
		t.Fatal("1st distinct area (N-2) refused")
	}
	if !b.ObserveArea("client", "s2") {
		t.Fatal("2nd distinct area (N-1) refused")
	}
	if !b.ObserveArea("client", "s3") {
		t.Fatal("3rd distinct area (N, the limit) refused")
	}
	if b.ObserveArea("client", "s4") {
		t.Error("4th distinct area (N+1) allowed")
	}
}

// TestSensorLimitBoundaryIsExclusive is the sensor-side twin of the area
// boundary test, at the sensor limit rather than the area limit.
func TestSensorLimitBoundaryIsExclusive(t *testing.T) {
	b, _ := breadth(t, 10, 2)

	if !b.ObserveSensor("client", 1) {
		t.Fatal("1st distinct sensor (N-1) refused")
	}
	if !b.ObserveSensor("client", 2) {
		t.Fatal("2nd distinct sensor (N, the limit) refused")
	}
	if b.ObserveSensor("client", 3) {
		t.Error("3rd distinct sensor (N+1) allowed")
	}
}

// TestTwoKeysDoNotShareASet pins that distinct client keys never share the
// underlying slug set — a bug that merged sets could let one client's
// visited areas silently pre-populate another's budget.
func TestTwoKeysDoNotShareASet(t *testing.T) {
	b, _ := breadth(t, 2, 10)

	b.ObserveArea("a", "sofia")
	b.ObserveArea("b", "sofia")
	b.ObserveArea("b", "varna")

	if got := b.SlugSetSizeForTesting("a"); got != 1 {
		t.Errorf("client a's slug set size = %d, want 1", got)
	}
	if got := b.SlugSetSizeForTesting("b"); got != 2 {
		t.Errorf("client b's slug set size = %d, want 2", got)
	}
}
