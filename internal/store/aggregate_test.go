package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

// The two type aliases keep the helper signatures short; contextT and poolT are
// declared once here rather than repeating the full types in every helper.
type contextT = context.Context
type poolT = *pgxpool.Pool

// seedArea inserts one area with a square polygon around the given centre.
// Parameterised, like every query in this project — no string concatenation,
// including in test helpers.
func seedArea(t *testing.T, ctx contextT, pool poolT, slug, kind string, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $1, $1,
		         ST_Buffer(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, 5000)::geography)`,
		slug, kind, lon, lat)
	if err != nil {
		t.Fatalf("seed area %s: %v", slug, err)
	}
}

func seedSensor(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'TEST', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
		 ON CONFLICT (sensor_id) DO NOTHING`,
		id, lon, lat)
	if err != nil {
		t.Fatalf("seed sensor %d: %v", id, err)
	}
}

func seedSensorReading(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64, metric string, value float64, quality string, at time.Time) {
	t.Helper()
	seedSensor(t, ctx, pool, id, lon, lat)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5::quality_flag)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE
		   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
		at, id, metric, value, quality)
	if err != nil {
		t.Fatalf("seed reading %d/%s: %v", id, metric, err)
	}
}

func assignAreas(t *testing.T, ctx contextT, pool poolT) {
	t.Helper()
	if _, _, err := area.AssignSensors(ctx, pool, testAssignTimeout); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}
}

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool
}

// TestAreaAggregatesRespectsCoverageThreshold is the central test of this task.
// Two sensors must NOT produce a published average, three must. Phase 1 §5.7:
// below the threshold, deeper tiers manufacture confident-looking averages from
// single sensors.
func TestAreaAggregatesRespectsCoverageThreshold(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "two-sensors", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "three-sensors", "oblast", 25.0, 43.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 2, 23.001, 42.001, "P2", 20, "ok", now)

	seedSensorReading(t, ctx, pool, 3, 25.0, 43.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 4, 25.001, 43.001, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 5, 25.002, 43.002, "P2", 30, "ok", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	byslug := map[string]store.AreaAggregate{}
	for _, a := range aggs {
		byslug[a.Slug] = a
	}

	two, ok := byslug["two-sensors"]
	if !ok {
		t.Fatal("two-sensors area missing from aggregates; an under-covered area must still be listed, so the map can render its insufficient-coverage state")
	}
	if two.Covered {
		t.Errorf("two-sensors Covered = true with 2 sensors; CoverageThreshold is %d", testStoreConfig().CoverageThreshold)
	}
	if len(two.Values) != 0 {
		t.Errorf("two-sensors published values %v; an uncovered area must publish no number at all", two.Values)
	}

	three, ok := byslug["three-sensors"]
	if !ok {
		t.Fatal("three-sensors area missing from aggregates")
	}
	if !three.Covered {
		t.Error("three-sensors Covered = false with 3 sensors; the threshold is a minimum, not an exclusive bound")
	}
	if got := three.Values["P2"]; got < 19.9 || got > 20.1 {
		t.Errorf("three-sensors P2 = %v, want 20 (mean of 10, 20, 30)", got)
	}

	// Bulgaria's lon range (22-29) and lat range (41-45) do not overlap, so
	// checking each coordinate against its OWN range — never as a combined
	// pair or distance tolerance — is what catches a lon/lat swap. A swapped
	// pair here would put CentroidLon around 43 (outside 22-29) and
	// CentroidLat around 25 (outside 41-45); both checks below would fail.
	assertInBulgaria(t, "two-sensors centroid", two.CentroidLon, two.CentroidLat)
	assertInBulgaria(t, "three-sensors centroid", three.CentroidLon, three.CentroidLat)
}

// assertInBulgaria checks lon and lat against their OWN ranges separately.
// Bulgaria spans lon 22-29, lat 41-45 — ranges that do not overlap, so a swap
// (lon and lat transposed) is detectable only by checking each axis on its own
// range, never as a combined pair or distance-from-expected tolerance.
func assertInBulgaria(t *testing.T, label string, lon, lat float64) {
	t.Helper()
	if lon < 22 || lon > 29 {
		t.Errorf("%s: lon = %v, want [22, 29]; a value in the lat range (41-45) means lon/lat are swapped", label, lon)
	}
	if lat < 41 || lat > 45 {
		t.Errorf("%s: lat = %v, want [41, 45]; a value in the lon range (22-29) means lon/lat are swapped", label, lat)
	}
}

// TestAreaAggregatesCountsDistinctSensorsNotRows pins "three readings from one
// sensor is not coverage" (brief, Phase 1 §5.7) directly: one sensor reporting
// several metrics must count as ONE sensor toward the threshold, not one per
// row. If count(DISTINCT sensor_id) were ever "optimised" to count(*), this
// area would wrongly cross CoverageThreshold on a single sensor.
func TestAreaAggregatesCountsDistinctSensorsNotRows(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "one-sensor-many-readings", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	for _, m := range []struct {
		name string
		v    float64
	}{
		{"P1", 30}, {"P2", 18}, {"temperature", 21}, {"humidity", 55}, {"pressure", 1013},
	} {
		seedSensorReading(t, ctx, pool, 40, 23.0, 42.0, m.name, m.v, "ok", now)
	}

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	if aggs[0].SensorCount != 1 {
		t.Errorf("SensorCount = %d, want 1 (5 readings from one sensor is not 5 sensors)", aggs[0].SensorCount)
	}
	if aggs[0].Covered {
		t.Error("Covered = true with 1 sensor reporting 5 metrics; a single sensor cannot cross CoverageThreshold no matter how many metrics it reports")
	}
	if len(aggs[0].Values) != 0 {
		t.Errorf("published values %v; an uncovered area must publish no number at all", aggs[0].Values)
	}
}

// TestAreaAggregatesCountsStationsNotDevices pins the count the reader sees.
// Upstream publishes one address as a particulate box AND a climate box, two
// sensor ids on the same coordinate, and the map draws one marker for the pair
// (snapshot.stationIDs). Counting ids made Varna's card say 44 over a map with
// 27 dots on it — and let three devices on one balcony cross a threshold that
// exists to refuse exactly that.
func TestAreaAggregatesCountsStationsNotDevices(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "one-address", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "three-addresses", "oblast", 25.0, 43.0)

	now := time.Now().UTC().Truncate(time.Minute)
	// Three devices, two of them at the identical published coordinate.
	seedSensorReading(t, ctx, pool, 50, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 51, 23.0, 42.0, "temperature", 21, "ok", now)
	seedSensorReading(t, ctx, pool, 52, 23.001, 42.0, "P2", 20, "ok", now)

	// Three devices at three addresses, for the other half of the claim: the
	// change must not deflate a count that was already right. Two of them share
	// a longitude and differ in latitude, so a grouping that looked at one axis
	// would merge two separate addresses and show up here.
	seedSensorReading(t, ctx, pool, 60, 25.0, 43.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 61, 25.0, 43.001, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 62, 25.002, 43.002, "P2", 30, "ok", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	byslug := map[string]store.AreaAggregate{}
	for _, a := range aggs {
		byslug[a.Slug] = a
	}

	one := byslug["one-address"]
	if one.SensorCount != 2 {
		t.Errorf("one-address SensorCount = %d, want 2; the two devices on one coordinate are one station and one marker", one.SensorCount)
	}
	if one.Covered {
		t.Errorf("one-address Covered = true on 2 stations; CoverageThreshold is %d, and it counts places, not boxes", testStoreConfig().CoverageThreshold)
	}

	three := byslug["three-addresses"]
	if three.SensorCount != 3 {
		t.Errorf("three-addresses SensorCount = %d, want 3", three.SensorCount)
	}
	if !three.Covered {
		t.Error("three-addresses Covered = false with 3 separate stations")
	}
}

// TestAreaAggregatesExcludesStaleReadings pins freshnessWindow: a reading
// older than the window must not count toward SensorCount or influence
// Values, even though the sensor otherwise looks perfectly healthy.
func TestAreaAggregatesExcludesStaleReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "stale-mix", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 50, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 51, 23.001, 42.001, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 52, 23.002, 42.002, "P2", 10, "ok", now)
	// Stale: outside freshnessWindow (2h), and far enough from 10 that if it
	// were counted, both SensorCount (4, not 3) and the mean would be wrong.
	seedSensorReading(t, ctx, pool, 53, 23.003, 42.003, "P2", 1000, "ok", now.Add(-3*time.Hour))

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	if aggs[0].SensorCount != 3 {
		t.Errorf("SensorCount = %d, want 3; the stale sensor (reading is 3h old) must not count", aggs[0].SensorCount)
	}
	if got := aggs[0].Values["P2"]; got < 9.9 || got > 10.1 {
		t.Errorf("P2 = %v, want 10; a value near 257 means the stale reading was counted", got)
	}
}

// TestLatestSensorsExcludesStaleReadings pins the same freshnessWindow rule
// for LatestSensors: a sensor whose only reading is stale must not appear at
// all in the result.
func TestLatestSensorsExcludesStaleReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 60, 23.0, 42.0, "P2", 15, "ok", now.Add(-3*time.Hour))

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	for _, sr := range sensors {
		if sr.SensorID == 60 {
			t.Fatalf("sensor 60 present with only a 3h-old reading; freshnessWindow (2h) must exclude it: %+v", sr)
		}
	}
}

// seedOfficialSensorReading is seedSensorReading for an EEA station. The id must
// be at or above store.OfficialSensorIDFloor: migration 00012 constrains source
// 'eea' and that range to mean the same thing.
func seedOfficialSensorReading(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64, metric string, value float64, at time.Time) {
	t.Helper()
	if id < store.OfficialSensorIDFloor {
		t.Fatalf("official sensor id %d is below the floor %d", id, store.OfficialSensorIDFloor)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location, source, source_ref)
		 VALUES ($1, 'EEA', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, 'eea', $4)
		 ON CONFLICT (sensor_id) DO NOTHING`,
		id, lon, lat, fmt.Sprintf("BG/SPO-TEST-%d", id))
	if err != nil {
		t.Fatalf("seed official sensor %d: %v", id, err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, 'ok')`,
		at, id, metric, value)
	if err != nil {
		t.Fatalf("seed official reading %d/%s: %v", id, metric, err)
	}
}

// EEA publishes hourly means stamped at the start of the hour, about an hour
// after the hour closes, so an official reading is already older than
// freshness_window when it arrives. Under one shared window the official layer
// is empty at every moment — which is what production showed.
func TestLatestSensorsKeepsOfficialStationsPastTheCommunityWindow(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	const officialID = store.OfficialSensorIDFloor + 7
	// Older than freshness_window (2h), inside official_freshness_window (12h).
	seedOfficialSensorReading(t, ctx, pool, officialID, 23.0, 42.0, "P1", 21, now.Add(-3*time.Hour))
	seedSensorReading(t, ctx, pool, 61, 23.0, 42.0, "P1", 15, "ok", now.Add(-3*time.Hour))

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	var sawOfficial, sawCommunity bool
	for _, sr := range sensors {
		switch sr.SensorID {
		case officialID:
			sawOfficial = true
		case 61:
			sawCommunity = true
		}
	}
	if !sawOfficial {
		t.Errorf("official station %d absent with a 3h-old reading; official_freshness_window (12h) must admit it", officialID)
	}
	// The wider window is for official stations only. Applying it to citizen
	// devices would leave dead sensors on the map for six hours.
	if sawCommunity {
		t.Errorf("community sensor 61 present with a 3h-old reading; freshness_window (2h) must exclude it")
	}
}

// seedReadingWithQuality adds one reading of any quality to a sensor that has
// already been seeded.
func seedReadingWithQuality(t *testing.T, ctx contextT, pool poolT, id int64, metric string, value float64, quality string, at time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5::quality_flag)`,
		at, id, metric, value, quality)
	if err != nil {
		t.Fatalf("seed %s reading %d/%s: %v", quality, id, metric, err)
	}
}

// The EEA collector writes the newest published hours as provisional rows
// flagged 'source_invalid'. Picking the newest row of ANY quality threw the
// last usable reading away, so every official station published an empty
// values object and the official layer drew nothing.
func TestLatestSensorsIgnoresANewerUnusableReading(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	const officialID = store.OfficialSensorIDFloor + 11
	seedOfficialSensorReading(t, ctx, pool, officialID, 23.0, 42.0, "P1", 21, now.Add(-8*time.Hour))
	seedReadingWithQuality(t, ctx, pool, officialID, "P1", 999, "source_invalid", now.Add(-1*time.Hour))

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	var got *store.SensorReading
	for i := range sensors {
		if sensors[i].SensorID == officialID {
			got = &sensors[i]
		}
	}
	if got == nil {
		t.Fatalf("official station %d absent; a station with a rejected newest row must still be listed", officialID)
	}
	if v, ok := got.Values["P1"]; !ok || v != 21 {
		t.Errorf("Values[P1] = %v (present %v), want 21: the newest USABLE row, not the masked one", v, ok)
	}
	// Measures says what the hardware measures, so it must stay unfiltered even
	// when every fresh reading for the metric was rejected.
	if !containsString(got.Measures, "P1") {
		t.Errorf("Measures = %v, want it to contain P1", got.Measures)
	}
	if got.Quality != "source_invalid" {
		t.Errorf("Quality = %q, want %q: the flag reports the newest row, of any quality", got.Quality, "source_invalid")
	}
}

// A metric whose only fresh reading is rejected has no value but is still
// measured — the panel says "no reading right now" rather than going silent.
func TestLatestSensorsKeepsMeasuresWhenEveryFreshReadingIsRejected(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 62, 23.0, 42.0, "P1", 10, "ok", now)
	seedReadingWithQuality(t, ctx, pool, 62, "P2", 900, "out_of_range", now)

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	var got *store.SensorReading
	for i := range sensors {
		if sensors[i].SensorID == 62 {
			got = &sensors[i]
		}
	}
	if got == nil {
		t.Fatalf("sensor 62 absent")
	}
	if _, ok := got.Values["P2"]; ok {
		t.Errorf("Values carries P2 = %v from an out_of_range reading", got.Values["P2"])
	}
	if !containsString(got.Measures, "P2") {
		t.Errorf("Measures = %v, want it to contain P2", got.Measures)
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestAreaAggregatesExcludesFlaggedReadings asserts the quality filter. Written
// so it fails if the filter is dropped: the flagged value is far enough from
// the good ones that including it moves the mean well outside tolerance.
func TestAreaAggregatesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "flagged", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 10, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 11, 23.001, 42.001, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 12, 23.002, 42.002, "P2", 10, "no_neighbours", now)
	// A stuck sensor reporting 1000: if the filter is missing, the mean jumps
	// from 10 to ~257 and the assertion below fails loudly.
	seedSensorReading(t, ctx, pool, 13, 23.003, 42.003, "P2", 1000, "stuck", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	// 'ok' and 'no_neighbours' are usable, 'stuck' is not — three usable
	// sensors, all reading 10.
	if got := aggs[0].Values["P2"]; got < 9.9 || got > 10.1 {
		t.Errorf("P2 = %v, want 10; a value near 257 means the quality filter is missing", got)
	}
	if aggs[0].SensorCount != 3 {
		t.Errorf("SensorCount = %d, want 3 (the stuck sensor must not count toward coverage either)", aggs[0].SensorCount)
	}
}

// TestSensorSeriesUsesRawBelowThirtyDays and its hourly counterpart pin the
// table-selection rule from Phase 1 §7.2. Getting it backwards is silent: both
// tables answer the query, one just returns nothing useful.
func TestSensorSeriesUsesRawBelowThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 20, 23.0, 42.0, "P2", 12, "ok", now.Add(-2*time.Hour))

	// A DIFFERENT value in reading_hourly for the same sensor and hour. If
	// SensorSeries reads the wrong table, it returns 99 and this fails.
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 20, 'P2', 99, 99, 99, 1)`,
		now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 20, "P2", now.Add(-24*time.Hour), nil, false, time.Second)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 12 {
		t.Errorf("value = %v, want 12; 99 means it read reading_hourly instead of reading", pts[0].Value)
	}
}

func TestSensorSeriesUsesHourlyAboveThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	bucket := now.Add(-60 * 24 * time.Hour)

	seedSensor(t, ctx, pool, 21, 23.0, 42.0)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 21, 'P2', 42, 40, 44, 6)`,
		bucket)
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 21, "P2", bucket.Add(-time.Hour), nil, true, time.Hour)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 42 {
		t.Errorf("value = %v, want 42", pts[0].Value)
	}
}

// TestLatestSensorsReturnsOneRowPerSensor guards against the classic
// join-fanout bug: four metrics on one sensor must produce one SensorReading
// with four values, not four SensorReadings.
func TestLatestSensorsReturnsOneRowPerSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Minute)
	for _, m := range []struct {
		name string
		v    float64
	}{
		{"P1", 30}, {"P2", 18}, {"temperature", 21}, {"humidity", 55},
	} {
		seedSensorReading(t, ctx, pool, 30, 23.0, 42.0, m.name, m.v, "ok", now)
	}

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	if len(sensors) != 1 {
		t.Fatalf("got %d sensors, want 1 (4 metrics on one sensor must not fan out into 4 rows)", len(sensors))
	}
	if len(sensors[0].Values) != 4 {
		t.Errorf("got %d values, want 4: %v", len(sensors[0].Values), sensors[0].Values)
	}
	if got := sensors[0].Values["P2"]; got != 18 {
		t.Errorf("P2 = %v, want 18", got)
	}
	assertInBulgaria(t, "sensor 30", sensors[0].Lon, sensors[0].Lat)
}

// TestLatestSensorsCarriesLifetime: the panel tells a reader how long this
// device has been reporting to us, so the row has to carry first_seen and
// last_seen from the sensor table — not the reading's own timestamp, and not
// each other.
func TestLatestSensorsCarriesLifetime(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Second)
	seedSensorReading(t, ctx, pool, 70, 23.0, 42.0, "P2", 12, "ok", now)

	first := now.Add(-90 * 24 * time.Hour)
	last := now.Add(-3 * time.Minute)
	if _, err := pool.Exec(ctx,
		`UPDATE sensor SET first_seen = $2, last_seen = $3 WHERE sensor_id = $1`,
		70, first, last); err != nil {
		t.Fatalf("set lifetime: %v", err)
	}

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	if len(sensors) != 1 {
		t.Fatalf("got %d sensors, want 1", len(sensors))
	}
	if got := sensors[0].FirstSeen.UTC(); !got.Equal(first) {
		t.Errorf("FirstSeen = %v, want %v", got, first)
	}
	if got := sensors[0].LastSeen.UTC(); !got.Equal(last) {
		t.Errorf("LastSeen = %v, want %v", got, last)
	}
}

// TestAreaSeriesAveragesAcrossSensors: the area series is the mean of the
// sensors in the area at each instant, not a concatenation of their readings.
// Concatenating would produce a sawtooth that looks like violent air-quality
// swings but is really just sensors disagreeing — the most misleading possible
// chart to publish under a public-health banner.
func TestAreaSeriesAveragesAcrossSensors(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	for i, v := range []float64{10, 30} {
		id := int64(700 + i)
		lon := 23.3219 + float64(i)*0.001
		seedSensorReading(t, ctx, pool, id, lon, 42.6977, "P2", v, "ok", base)
	}
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), nil, false, time.Second)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (two sensors at one instant must average into one point)", len(points))
	}
	if points[0].Value != 20 {
		t.Errorf("value = %v, want 20 (the mean of 10 and 30)", points[0].Value)
	}
}

// TestAreaSeriesBucketsAsynchronousReports: the one above seeds both sensors at
// the SAME instant, which is the assumption that hid a real defect — grouping on
// the raw timestamp passed it while aggregating nothing in production, because
// real sensors report asynchronously at second resolution and equality on
// r.time almost never matches two rows.
//
// This seeds them a second apart, which is what the network actually does. It
// fails against a query that groups on r.time (two points, 10 and 30) and
// passes only against one that groups on a bucket.
func TestAreaSeriesBucketsAsynchronousReports(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 710, 23.3219, 42.6977, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 711, 23.3229, 42.6977, "P2", 30, "ok", base.Add(time.Second))
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), nil, false, 5*time.Minute)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (two sensors one second apart share a 5-minute bucket)", len(points))
	}
	if points[0].Value != 20 {
		t.Errorf("value = %v, want 20 (the mean of 10 and 30)", points[0].Value)
	}
}

// TestAreaSeriesSeparatesDistinctBuckets is the other half of the one above: a
// bucket that merged everything would pass that test for the wrong reason.
func TestAreaSeriesSeparatesDistinctBuckets(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 720, 23.3219, 42.6977, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 721, 23.3229, 42.6977, "P2", 30, "ok", base.Add(20*time.Minute))
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), nil, false, 5*time.Minute)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2 (readings 20 minutes apart are different 5-minute buckets)", len(points))
	}
}

// TestAreaSeriesExcludesFlaggedReadings: the same quality filter the aggregates
// use must apply here, or a chart shows values the map refuses to.
func TestAreaSeriesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 800, 23.3219, 42.6977, "P2", 10, "ok", base)
	// A stuck sensor pegged at 1000. If the filter is dropped the mean becomes
	// 505 rather than 10 — a 50x error, impossible to miss.
	seedSensorReading(t, ctx, pool, 801, 23.3229, 42.6977, "P2", 1000, "stuck", base)
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), nil, false, time.Second)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 10 {
		t.Errorf("value = %v, want 10; a flagged reading was included", points[0].Value)
	}
}

// seedAreaSeriesFixture seeds two sensors in one area and one sensor in
// another, plus an out-of-range reading in the first area, so the parity test
// exercises both the (slug, time) grouping and the quality filter rather than
// assuming either.
func seedAreaSeriesFixture(t *testing.T, ctx contextT, pool poolT) {
	t.Helper()
	seedArea(t, ctx, pool, "series-a", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "series-b", "oblast", 25.0, 43.0)

	base := time.Now().UTC().Truncate(time.Minute)
	// Two sensors in series-a reporting a second apart, the way the network
	// actually behaves. Seeding both at one instant would let a query that
	// grouped on the raw timestamp pass this parity test while aggregating
	// nothing, which is exactly how the defect this fixture now guards survived.
	seedSensorReading(t, ctx, pool, 200, 23.0, 42.0, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 201, 23.001, 42.001, "P2", 20, "ok", base.Add(time.Second))
	// A flagged reading in series-a. If the quality filter were dropped, this
	// would move AllAreaSeries's mean away from AreaSeries's, and the parity
	// test would fail on the point value rather than passing by accident
	// because the fixture had nothing bad in it.
	seedSensorReading(t, ctx, pool, 203, 23.002, 42.002, "P2", 999, "out_of_range", base.Add(2*time.Second))
	// One sensor in series-b.
	seedSensorReading(t, ctx, pool, 202, 25.0, 43.0, "P2", 30, "ok", base)

	assignAreas(t, ctx, pool)
}

// seedTwoSensorsOneInstant seeds one area with two sensors reporting the given
// values at the same instant, and returns the area's slug and that instant.
func seedTwoSensorsOneInstant(t *testing.T, ctx contextT, pool poolT, v1, v2 float64) (string, time.Time) {
	t.Helper()
	const slug = "one-instant"
	seedArea(t, ctx, pool, slug, "oblast", 24.0, 42.5)
	at := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 300, 24.0, 42.5, "P2", v1, "ok", at)
	seedSensorReading(t, ctx, pool, 301, 24.001, 42.501, "P2", v2, "ok", at)
	assignAreas(t, ctx, pool)
	return slug, at
}

// TestAllAreaSeriesMatchesThePerAreaQuery is the anti-drift test. AllAreaSeries
// and AreaSeries are two SQL statements that must produce the same numbers, and
// the snapshot path only serves the right data for as long as they agree. Any
// future edit to one that is not mirrored in the other fails here rather than
// silently shipping a chart that disagrees with the database-backed fall-through.
func TestAllAreaSeriesMatchesThePerAreaQuery(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	// Two sensors in one area, one in another, and two readings at the SAME
	// timestamp so the grouping is exercised rather than assumed.
	seedAreaSeriesFixture(t, ctx, pool)

	since := time.Now().UTC().Add(-24 * time.Hour)
	all, err := s.AllAreaSeries(ctx, "P2", since, false, 5*time.Minute)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("AllAreaSeries returned no areas; the fixture is not being seen, so this test proves nothing")
	}

	for slug, batched := range all {
		single, err := s.AreaSeries(ctx, slug, "P2", since, nil, false, 5*time.Minute)
		if err != nil {
			t.Fatalf("AreaSeries(%q): %v", slug, err)
		}
		if len(batched) != len(single) {
			t.Fatalf("area %q: AllAreaSeries returned %d points, AreaSeries returned %d", slug, len(batched), len(single))
		}
		for i := range single {
			if !batched[i].Time.Equal(single[i].Time) {
				t.Errorf("area %q point %d: time = %v, want %v", slug, i, batched[i].Time, single[i].Time)
			}
			if batched[i].Value != single[i].Value {
				t.Errorf("area %q point %d: value = %v, want %v", slug, i, batched[i].Value, single[i].Value)
			}
		}
	}
}

// TestAllAreaSeriesGroupsSensorsAtTheSameInstant pins the averaging directly.
// Without the (slug, time) grouping the result is every sensor's reading in
// timestamp order, which renders as a sawtooth a reader would interpret as
// rapid air-quality swings rather than as two sensors disagreeing.
func TestAllAreaSeriesGroupsSensorsAtTheSameInstant(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20) // one area, one timestamp, values 10 and 20

	points, err := s.AllAreaSeries(ctx, "P2", at.Add(-time.Hour), false, time.Second)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	got := points[slug]
	if len(got) != 1 {
		t.Fatalf("got %d points for %q, want 1 — the two sensors were not grouped", len(got), slug)
	}
	if got[0].Value != 15 {
		t.Errorf("value = %v, want 15 (the mean of 10 and 20)", got[0].Value)
	}
}

// TestAllAreaSeriesCapsRowCount seeds one more bucket than
// store.AllAreaSeriesRowLimit for a single area and asserts the result is
// truncated to exactly the cap. AllAreaSeries has no per-caller since/until
// tightening the way AreaSeries does, so the LIMIT is the only thing standing
// between a wide window and an unbounded result set.
func TestAllAreaSeriesCapsRowCount(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "capped", "oblast", 23.0, 42.0)
	seedSensor(t, ctx, pool, 1, 23.0, 42.0)
	assignAreas(t, ctx, pool)

	start := time.Now().UTC().Add(-24 * time.Hour)
	const extra = 500
	rows := store.AllAreaSeriesRowLimit + extra
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 SELECT $1::timestamptz + (n || ' seconds')::interval, $2, 'P2', 10, 'ok'::quality_flag
		   FROM generate_series(0, $3) AS n`,
		start, int64(1), rows-1); err != nil {
		t.Fatalf("bulk seed: %v", err)
	}

	all, err := s.AllAreaSeries(ctx, "P2", start.Add(-time.Minute), false, time.Second)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	if got := len(all["capped"]); got != store.AllAreaSeriesRowLimit {
		t.Errorf("AllAreaSeries returned %d points, want exactly %d (the row cap); seeded %d",
			got, store.AllAreaSeriesRowLimit, rows)
	}
}

// TestAllAreaSeriesTimesOutUnderItsOwnScopedBound mirrors the AreaSeries and
// SensorSeries timeout tests: AllAreaSeries reads across every area in one
// query and must not inherit the pool-wide 15s statement_timeout either.
func TestAllAreaSeriesTimesOutUnderItsOwnScopedBound(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	if pool.Config().MaxConns < 2 {
		t.Fatalf("pool MaxConns = %d, want >= 2 so the blocker and AllAreaSeries use distinct connections", pool.Config().MaxConns)
	}

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)
	_ = slug

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker Begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE reading IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("LOCK TABLE: %v", err)
	}

	start := time.Now()
	_, err = s.AllAreaSeries(ctx, "P2", at.Add(-time.Hour), false, time.Second)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("AllAreaSeries err = %v, want SQLSTATE 57014 (query_canceled)", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the pool's 15s bound applied, not the scoped 5s", elapsed)
	}
}

// TestAreaSeriesExcludesOutOfRangeNaN: deferred item (b) — a faulty sensor can
// report NaN, and strconv.ParseFloat("nan", ...) succeeds while NaN compares
// false against every < and > in a plain range check. The ingest-time
// out_of_range flag (internal/quality) is what actually rejects it — InRange
// returns false for NaN because NaN >= min and NaN <= max are both false — and
// this test pins that AreaSeries' quality filter honours that flag rather than
// re-deriving its own numeric check that a NaN could slip past.
func TestAreaSeriesExcludesOutOfRangeNaN(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "nan-area", "oblast", 23.5, 42.5)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 900, 23.5, 42.5, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 901, 23.501, 42.5, "P2", math.NaN(), "out_of_range", base)
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "nan-area", "P2", base.Add(-time.Hour), nil, false, time.Second)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 10 {
		t.Errorf("value = %v, want 10; a NaN reading flagged out_of_range leaked into the average", points[0].Value)
	}
}

// TestAreaSeriesTimesOutUnderItsOwnScopedBound drives AreaSeries itself,
// rather than db.SetLocalStatementTimeout and pg_sleep in isolation. An
// earlier version of this test (TestSeriesQueriesUseTheShortStatementTimeout)
// called the db helper and ran an ad-hoc pg_sleep directly, never AreaSeries
// or SensorSeries — so it could not have caught a missing timeout call in
// either function, which is exactly the mutation review found inert.
//
// A second session locks the reading table ACCESS EXCLUSIVE and holds it open
// for the whole test, so AreaSeries' own query blocks waiting for the lock.
// Its own scoped statement_timeout — not the pool's 15s default — must abort
// that wait at 5s.
func TestAreaSeriesTimesOutUnderItsOwnScopedBound(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	// Two live connections are required: one held open by the blocker's
	// transaction, one for AreaSeries' own Begin. Asserted, not assumed — the
	// pool defaults to more than one, but a future change to that default
	// must not silently turn this into a test that deadlocks on itself.
	if pool.Config().MaxConns < 2 {
		t.Fatalf("pool MaxConns = %d, want >= 2 so the blocker and AreaSeries use distinct connections", pool.Config().MaxConns)
	}

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)

	// Another session holds ACCESS EXCLUSIVE on reading; AreaSeries' own
	// statement_timeout must abort it, not the pool's 15s.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker Begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE reading IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("LOCK TABLE: %v", err)
	}

	start := time.Now()
	_, err = s.AreaSeries(ctx, slug, "P2", at.Add(-time.Hour), nil, false, time.Second)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("AreaSeries err = %v, want SQLSTATE 57014 (query_canceled)", err)
	}
	// The bound must be the scoped 5s, not the pool's 15s default.
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the pool's 15s bound applied, not the scoped 5s", elapsed)
	}
}

// TestSensorSeriesTimesOutUnderItsOwnScopedBound mirrors the AreaSeries test
// above for SensorSeries: same lock, same assertion, the other database-backed
// series query that got the same scoped-timeout treatment.
func TestSensorSeriesTimesOutUnderItsOwnScopedBound(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	if pool.Config().MaxConns < 2 {
		t.Fatalf("pool MaxConns = %d, want >= 2 so the blocker and SensorSeries use distinct connections", pool.Config().MaxConns)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 950, 23.5, 42.5, "P2", 10, "ok", now)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker Begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE reading IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("LOCK TABLE: %v", err)
	}

	start := time.Now()
	_, err = s.SensorSeries(ctx, 950, "P2", now.Add(-time.Hour), nil, false, time.Second)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("SensorSeries err = %v, want SQLSTATE 57014 (query_canceled)", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the pool's 15s bound applied, not the scoped 5s", elapsed)
	}
}

// TestAreaSeriesStillReturnsDataInsideItsTransaction. Wrapping a read in a
// transaction is exactly the kind of change that can return an empty result set
// while every timeout test still passes — a chart that is blank rather than
// wrong, which is harder to notice in review than a failure.
func TestAreaSeriesStillReturnsDataInsideItsTransaction(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)

	points, err := s.AreaSeries(ctx, slug, "P2", at.Add(-time.Hour), nil, false, time.Second)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 15 {
		t.Errorf("value = %v, want 15", points[0].Value)
	}
}

// One faulty box reading 900 µg/m³ beside four neighbours in the teens used to
// carry a whole province: a mean answers 189, which is a figure nowhere in the
// province and paints every map cell in it the worst colour. A median answers
// what the province is actually breathing.
//
// The wild sensor is seeded 'ok': the ingest spatial check cannot flag a sensor
// that has too few neighbours to compare against, so the published aggregate is
// the last place this can be caught.
func TestAreaAggregatesResistOneWildSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	now := time.Now().UTC().Truncate(time.Minute)
	for i, v := range []float64{10, 11, 12, 13, 900} {
		seedSensorReading(t, ctx, pool, int64(900+i), 24.7+float64(i)*0.001, 41.57, "P2", v, "ok", now)
	}
	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("areas = %d, want 1", len(aggs))
	}
	if got := aggs[0].Values["P2"]; got != 12 {
		t.Errorf("P2 = %v, want 12 — the median of 10, 11, 12, 13, 900 (the mean is 189)", got)
	}
}

// The same rule on the chart the area page draws: a line that spikes because one
// device failed tells the reader the air changed when it did not.
func TestAreaSeriesResistsOneWildSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i, v := range []float64{10, 12, 900} {
		seedSensorReading(t, ctx, pool, int64(950+i), 24.7+float64(i)*0.001, 41.57, "P2", v, "ok", base.Add(time.Duration(i)*time.Second))
	}
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "smolyan", "P2", base.Add(-time.Hour), nil, false, time.Minute)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	if points[0].Value != 12 {
		t.Errorf("value = %v, want 12 — the median of 10, 12 and 900", points[0].Value)
	}
}

// A sensor reporting twice inside one bucket must not get two votes in the
// median: the point is the middle SENSOR, not the middle row, or a chatty
// device decides the area's figure by reporting more often than its neighbours.
func TestAreaSeriesTakesTheMiddleSensorNotTheMiddleRow(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	// Two quiet sensors, one chatty sensor reporting three times high.
	seedSensorReading(t, ctx, pool, 960, 24.700, 41.57, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 961, 24.701, 41.57, "P2", 12, "ok", base.Add(time.Second))
	for i, v := range []float64{900, 902, 904} {
		seedSensorReading(t, ctx, pool, 962, 24.702, 41.57, "P2", v, "ok", base.Add(time.Duration(2+i)*time.Second))
	}
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "smolyan", "P2", base.Add(-time.Hour), nil, false, time.Minute)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	if points[0].Value != 12 {
		t.Errorf("value = %v, want 12 — three rows from one device are one vote", points[0].Value)
	}
}

// The band's median must be the same number AreaSeries reports for the bucket,
// and its extremes must be sensors: a chatty device's individual readings must
// not become the low and the high of a bucket it is the only member of.
func TestAreaSeriesBandTakesTheExtremeSensorsNotTheExtremeRows(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 970, 24.700, 41.57, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 971, 24.701, 41.57, "P2", 12, "ok", base.Add(time.Second))
	// One device swinging between 20 and 40 averages to 30 — so 40 is never the
	// bucket's high, and 20 is never its low.
	for i, v := range []float64{20, 40} {
		seedSensorReading(t, ctx, pool, 972, 24.702, 41.57, "P2", v, "ok", base.Add(time.Duration(2+i)*time.Second))
	}
	assignAreas(t, ctx, pool)

	bands, err := s.AreaSeriesBand(ctx, "smolyan", "P2", base.Add(-time.Hour), nil, false, time.Minute)
	if err != nil {
		t.Fatalf("AreaSeriesBand: %v", err)
	}
	if len(bands) != 1 {
		t.Fatalf("bands = %d, want 1", len(bands))
	}
	if bands[0].Low != 10 || bands[0].Median != 12 || bands[0].High != 30 {
		t.Errorf("band = low %v, median %v, high %v; want 10, 12, 30",
			bands[0].Low, bands[0].Median, bands[0].High)
	}

	points, err := s.AreaSeries(ctx, "smolyan", "P2", base.Add(-time.Hour), nil, false, time.Minute)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 || points[0].Value != bands[0].Median {
		t.Errorf("the band's median (%v) is not the series' value (%v)", bands[0].Median, points)
	}
}

// The band inherits the quality filter from the same subquery the median uses.
// A flagged reading counted here would put a rejected value on the chart as the
// area's worst sensor — the one line a reader looks at first.
func TestAreaSeriesBandExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "smolyan", "oblast", 24.7, 41.57)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 980, 24.700, 41.57, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 981, 24.701, 41.57, "P2", 14, "ok", base.Add(time.Second))
	seedSensorReading(t, ctx, pool, 982, 24.702, 41.57, "P2", 900, "out_of_range", base.Add(2*time.Second))
	assignAreas(t, ctx, pool)

	bands, err := s.AreaSeriesBand(ctx, "smolyan", "P2", base.Add(-time.Hour), nil, false, time.Minute)
	if err != nil {
		t.Fatalf("AreaSeriesBand: %v", err)
	}
	if len(bands) != 1 {
		t.Fatalf("bands = %d, want 1", len(bands))
	}
	if bands[0].High != 14 {
		t.Errorf("high = %v, want 14 — the flagged 900 reading is not a sensor", bands[0].High)
	}
}

// geojsonFeatureCount reports the number of features in a committed boundary
// file — the real source of an area's existence, not a hardcoded guess.
func geojsonFeatureCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return len(doc.Features)
}

// TestAllAreaSeriesRowLimitCoversRealConfig is the structural killing test for
// fix-round-1 item 1: AllAreaSeriesRowLimit must stay comfortably above the
// worst case AllAreaSeries can actually be asked for, computed from the real
// airbg.yaml and the real committed boundary files rather than a hardcoded
// number that rots the moment either changes.
//
// Area count is city + oblast + neighbourhood boundaries only: AssignSensers
// excludes area.kind = NationalBoundaryKind ("country") from area_sensor, so
// the country boundary can never contribute a row to AllAreaSeries no matter
// how many features bulgaria.geojson has.
func TestAllAreaSeriesRowLimitCoversRealConfig(t *testing.T) {
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	var maxBuckets int
	for name, pd := range cfg.Series.Periods {
		if pd.Bucket <= 0 {
			t.Fatalf("period %q has no bucket duration; this test proves nothing", name)
		}
		buckets := int((pd.Window + pd.Bucket - 1) / pd.Bucket) // ceil
		if buckets > maxBuckets {
			maxBuckets = buckets
		}
	}
	if maxBuckets == 0 {
		t.Fatal("no series period found in airbg.yaml; this test proves nothing")
	}

	areaCount := 0
	for _, f := range []string{"oblasti.geojson", "cities.geojson", "sofia-districts.geojson"} {
		areaCount += geojsonFeatureCount(t, filepath.Join("..", "..", "data", "boundaries", f))
	}
	if areaCount == 0 {
		t.Fatal("no boundary features found; this test proves nothing")
	}

	worst := maxBuckets * areaCount
	// "Comfortably below": the limit must be at least 4x the worst case, not
	// just barely above it — a limit sized to exactly today's worst case is
	// exactly what silently broke in fix-round-1 item 1 once the area count
	// grew a little.
	if worst*4 > store.AllAreaSeriesRowLimit {
		t.Errorf("worst case = %d buckets x %d areas = %d rows; want AllAreaSeriesRowLimit (%d) at least 4x that, got %.1fx",
			maxBuckets, areaCount, worst, store.AllAreaSeriesRowLimit, float64(store.AllAreaSeriesRowLimit)/float64(worst))
	}
}

// findAgg picks one area out of an AreaAggregates result.
func findAgg(t *testing.T, aggs []store.AreaAggregate, slug string) store.AreaAggregate {
	t.Helper()
	for _, a := range aggs {
		if a.Slug == slug {
			return a
		}
	}
	t.Fatalf("no aggregate for %q in %d rows", slug, len(aggs))
	return store.AreaAggregate{}
}

func TestAreaAggregatesBreakDownByNetwork(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)
	now := time.Now().UTC().Truncate(time.Minute)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	seedSensorReading(t, ctx, pool, 101, 23.3219, 42.6977, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 102, 23.3229, 42.6977, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 103, 23.3239, 42.6977, "P2", 30, "ok", now)
	seedOfficialSensorReading(t, ctx, pool, store.OfficialSensorIDFloor+1, 23.3249, 42.6977, "P2", 100, now)
	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	a := findAgg(t, aggs, "sofia")

	sc, ok := a.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", a.BySource)
	}
	if sc.N != 3 || sc.Values["P2"] != 20 {
		t.Errorf("sensor.community = {n:%d P2:%v}, want {n:3 P2:20}", sc.N, sc.Values["P2"])
	}
	eea, ok := a.BySource["eea"]
	if !ok {
		t.Fatalf("by_source has no eea: %#v", a.BySource)
	}
	if eea.N != 1 || eea.Values["P2"] != 100 {
		t.Errorf("eea = {n:%d P2:%v}, want {n:1 P2:100}", eea.N, eea.Values["P2"])
	}
	// The median of all four, not of either network: 10, 20, 30, 100 -> 25.
	if a.Values["P2"] != 25 {
		t.Errorf("values.P2 = %v, want 25 — the blended median must not move", a.Values["P2"])
	}
	// Two networks at one coordinate are two stations per-source and one in the
	// total, so the parts bound the total rather than summing to it.
	if sc.N < 1 || eea.N < 1 {
		t.Errorf("per-source n = %d / %d, want at least 1 each", sc.N, eea.N)
	}
	if a.SensorCount < sc.N || a.SensorCount < eea.N {
		t.Errorf("SensorCount %d is below a per-source n (%d, %d)", a.SensorCount, sc.N, eea.N)
	}
}

// The two network keys are all SQL can produce: sourceExpr is a total CASE over
// the sensor id, so no third key and no blank key can reach a caller.
func TestAreaAggregatesNameOnlyTheTwoKnownNetworks(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)
	now := time.Now().UTC().Truncate(time.Minute)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	seedSensorReading(t, ctx, pool, 111, 23.3219, 42.6977, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 112, 23.3229, 42.6977, "P2", 20, "ok", now)
	seedOfficialSensorReading(t, ctx, pool, store.OfficialSensorIDFloor+11, 23.3239, 42.6977, "P2", 90, now)
	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	keys := make([]string, 0, 2)
	for k := range findAgg(t, aggs, "sofia").BySource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"eea", "sensor.community"}) {
		t.Errorf("network keys = %q, want exactly [eea sensor.community]", keys)
	}
}

func TestAreaAggregatesPerSourceCountsStationsNotDevices(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)
	now := time.Now().UTC().Truncate(time.Minute)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	// Two devices at ONE address, plus two more addresses.
	seedSensorReading(t, ctx, pool, 201, 23.3219, 42.6977, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 202, 23.3219, 42.6977, "humidity", 55, "ok", now)
	seedSensorReading(t, ctx, pool, 203, 23.3229, 42.6977, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 204, 23.3239, 42.6977, "P2", 30, "ok", now)
	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if got := findAgg(t, aggs, "sofia").BySource["sensor.community"].N; got != 3 {
		t.Errorf("sensor.community n = %d, want 3 — four devices at three addresses", got)
	}
}

func TestAreaAggregatesPerSourceIsEmptyWithoutCoverage(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)
	now := time.Now().UTC().Truncate(time.Minute)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	seedSensorReading(t, ctx, pool, 301, 23.3219, 42.6977, "P2", 10, "ok", now)
	seedOfficialSensorReading(t, ctx, pool, store.OfficialSensorIDFloor+21, 23.3229, 42.6977, "P2", 90, now)
	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	a := findAgg(t, aggs, "sofia")
	// testStoreConfig's CoverageThreshold is 3; two stations is below it.
	if a.Covered {
		t.Fatalf("two stations should be below the coverage threshold; got Covered = true")
	}
	if len(a.BySource) != 0 {
		t.Errorf("BySource = %#v, want empty for an uncovered area", a.BySource)
	}
}

// The windowed answer must break down the same way the live one does: the two
// are assembled from the same projection and may differ only in the value.
func TestWindowedAreaAggregatesBreakDownByNetwork(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)
	now := time.Now().UTC().Truncate(time.Minute)
	bucket := now.Truncate(time.Hour)
	official := store.OfficialSensorIDFloor + 31

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	seedSensorReading(t, ctx, pool, 401, 23.3219, 42.6977, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 402, 23.3229, 42.6977, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 403, 23.3239, 42.6977, "P2", 30, "ok", now)
	seedOfficialSensorReading(t, ctx, pool, official, 23.3249, 42.6977, "P2", 100, now)
	seedHourly(t, ctx, pool, 401, "P2", bucket, 10, 1)
	seedHourly(t, ctx, pool, 402, "P2", bucket, 20, 1)
	seedHourly(t, ctx, pool, 403, "P2", bucket, 30, 1)
	seedHourly(t, ctx, pool, official, "P2", bucket, 100, 1)
	assignAreas(t, ctx, pool)

	aggs, err := s.WindowedAreaAggregates(ctx, []string{"oblast"}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("WindowedAreaAggregates: %v", err)
	}
	a := findAgg(t, aggs, "sofia")
	if a.BySource["eea"].N != 1 || a.BySource["sensor.community"].N != 3 {
		t.Errorf("windowed BySource = %#v, want the same 3/1 split as the live query", a.BySource)
	}
	if a.BySource["eea"].Values["P2"] != 100 {
		t.Errorf("windowed eea P2 = %v, want 100", a.BySource["eea"].Values["P2"])
	}
}
