package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

func seedHourly(t *testing.T, ctx contextT, pool poolT, id int64, metric string, bucket time.Time, avg float64, samples int) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, $2, $3, $4, $4, $4, $5)`,
		bucket.Truncate(time.Hour), id, metric, avg, samples)
	if err != nil {
		t.Fatalf("seed reading_hourly %d/%s: %v", id, metric, err)
	}
}

// The weighting is the reason this reads reading_hourly rather than avg()ing the
// hourly averages: an hour with two samples and an hour with sixty are not the
// same evidence, and a flat mean of 10 and 30 here would answer 20 where the
// underlying readings average 29.35.
func TestWindowedSensorsWeightsHoursBySampleCount(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 99, "ok", now)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-2*time.Hour), 10, 2)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 30, 60)

	got, err := s.WindowedSensors(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedSensors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sensors = %d, want 1", len(got))
	}
	// (10*2 + 30*60) / 62 = 29.35
	if v := got[0].Values["P2"]; v < 29.34 || v > 29.36 {
		t.Errorf("P2 = %v, want 29.35; a flat mean of the hourly averages gives 20", v)
	}
}

// Widening the window must never remove a marker. A device first seen an hour
// ago has a live reading and no rollup row a week back, and dropping it would
// make the map emptier the more history the reader asked for.
func TestWindowedSensorsKeepsADeviceWithNoRollupRow(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 42, "ok", now)

	got, err := s.WindowedSensors(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedSensors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("sensors = %d, want 1; the device must survive the window join", len(got))
	}
	if _, ok := got[0].Values["P2"]; ok {
		t.Errorf("Values = %v, want no P2 entry; there is no windowed reading to publish", got[0].Values)
	}
	if len(got[0].Measures) != 1 || got[0].Measures[0] != "P2" {
		t.Errorf("Measures = %v, want [P2]; what the device measures does not depend on the window", got[0].Measures)
	}
}

func TestWindowedSensorsIgnoresBucketsBeforeTheWindow(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 99, "ok", now)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-50*time.Hour), 100, 10)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 10, 10)

	got, err := s.WindowedSensors(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedSensors: %v", err)
	}
	if v := got[0].Values["P2"]; v < 9.9 || v > 10.1 {
		t.Errorf("P2 = %v, want 10; 55 means the 50-hour-old bucket leaked into a 24h window", v)
	}
}

// A device that stopped reporting is absent from the live answer, and must stay
// absent from the windowed one however much history it left behind. Otherwise
// widening the window resurrects dead sensors as if they were reporting now.
func TestWindowedSensorsExcludesStaleDevices(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 10, "ok", now.Add(-72*time.Hour))
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 10, 10)

	got, err := s.WindowedSensors(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedSensors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("sensors = %d, want 0; freshness is the live rule in both queries", len(got))
	}
}

func TestWindowedAreaAggregatesAveragesTheWindow(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "covered", "oblast", 25.0, 43.0)
	now := time.Now().UTC().Truncate(time.Hour)
	for i, live := range []float64{100, 200, 300} {
		id := int64(i + 1)
		lon, lat := 25.0+float64(i)/1000, 43.0+float64(i)/1000
		seedSensorReading(t, ctx, pool, id, lon, lat, "P2", live, "ok", now)
		seedHourly(t, ctx, pool, id, "P2", now.Add(-time.Hour), float64(10*(i+1)), 6)
	}
	assignAreas(t, ctx, pool)

	aggs, err := s.WindowedAreaAggregates(ctx, []string{"oblast"}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedAreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("areas = %d, want 1", len(aggs))
	}
	if got := aggs[0].Values["P2"]; got < 19.9 || got > 20.1 {
		t.Errorf("P2 = %v, want 20 (mean of 10, 20, 30); 200 means it published the live reading", got)
	}
}

// The whole point of joining the window onto the live CTE: the reader switching
// window sees different numbers over the same set of areas, with the same
// station counts and the same coverage verdicts.
func TestWindowedAreaAggregatesMatchesLiveShape(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "covered", "oblast", 25.0, 43.0)
	seedArea(t, ctx, pool, "thin", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "empty", "oblast", 27.0, 44.0)

	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 3; i++ {
		id := int64(i + 1)
		seedSensorReading(t, ctx, pool, id, 25.0+float64(i)/1000, 43.0+float64(i)/1000, "P2", 50, "ok", now)
		seedHourly(t, ctx, pool, id, "P2", now.Add(-time.Hour), 5, 6)
	}
	// One station in "thin": below the threshold, so it must publish nothing in
	// either answer even though it has a windowed average to publish.
	seedSensorReading(t, ctx, pool, 9, 23.0, 42.0, "P2", 50, "ok", now)
	seedHourly(t, ctx, pool, 9, "P2", now.Add(-time.Hour), 5, 6)
	assignAreas(t, ctx, pool)

	live, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	windowed, err := s.WindowedAreaAggregates(ctx, []string{"oblast"}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedAreaAggregates: %v", err)
	}
	if len(live) != len(windowed) {
		t.Fatalf("areas: live %d, windowed %d; the two must list the same areas", len(live), len(windowed))
	}
	for i := range live {
		l, w := live[i], windowed[i]
		if l.Slug != w.Slug {
			t.Fatalf("area %d: live %q, windowed %q; both are ordered by slug", i, l.Slug, w.Slug)
		}
		if l.SensorCount != w.SensorCount || l.Covered != w.Covered {
			t.Errorf("%s: live (%d stations, covered %v), windowed (%d stations, covered %v)",
				l.Slug, l.SensorCount, l.Covered, w.SensorCount, w.Covered)
		}
		if !l.Covered && len(w.Values) != 0 {
			t.Errorf("%s: uncovered area published %v over the window", l.Slug, w.Values)
		}
	}
	if got := windowed[0].Values["P2"]; got < 4.9 || got > 5.1 {
		t.Errorf("covered P2 = %v, want 5 (the window), not 50 (now)", got)
	}
}

// The windowed area figure is a median too, for the same reason the live one is:
// a device stuck high for a week would otherwise carry the whole province's
// 24-hour and 7-day numbers.
func TestWindowedAreaAggregatesResistOneWildSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	now := time.Now().UTC().Truncate(time.Minute)
	for i, v := range []float64{10, 11, 12, 13, 900} {
		id := int64(970 + i)
		seedSensorReading(t, ctx, pool, id, 24.7+float64(i)*0.001, 41.57, "P2", v, "ok", now)
		seedHourly(t, ctx, pool, id, "P2", now.Add(-time.Hour), v, 60)
	}
	assignAreas(t, ctx, pool)

	aggs, err := s.WindowedAreaAggregates(ctx, []string{"oblast"}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedAreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("areas = %d, want 1", len(aggs))
	}
	if got := aggs[0].Values["P2"]; got != 12 {
		t.Errorf("P2 = %v, want 12 — the median of the five devices' window means", got)
	}
}
