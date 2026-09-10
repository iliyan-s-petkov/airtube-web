package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// testStoreConfig mirrors airbg.yaml so existing coverage assertions keep
// asserting the same threshold.
func testStoreConfig() config.Store {
	return config.Store{
		CoverageThreshold:       3,
		FreshnessWindow:         2 * time.Hour,
		OfficialFreshnessWindow: 6 * time.Hour,
	}
}

// testSeriesTimeout and testAssignTimeout mirror airbg.yaml's
// database.statement_timeouts.series and .assign.
const (
	testSeriesTimeout = 5 * time.Second
	testAssignTimeout = 60 * time.Second
)

func newStore(t *testing.T) (context.Context, *pgxpool.Pool, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool, store.New(pool, testStoreConfig(), testSeriesTimeout)
}

func sample(id int64, metric string, value float64, flag quality.Flag, ts time.Time) quality.Scored {
	return quality.Scored{
		Reading: upstream.Reading{
			SensorID:   id,
			SensorType: "SDS011",
			Lon:        23.3327,
			Lat:        42.6957,
			Metric:     metric,
			Value:      value,
			Timestamp:  ts,
		},
		Flag: flag,
	}
}

func TestUpsertSensorsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(1, "P2", 16.1, quality.FlagOK, ts),
	}

	for i := 0; i < 2; i++ {
		if err := s.UpsertSensors(ctx, scored, nil); err != nil {
			t.Fatalf("UpsertSensors: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("sensor count = %d, want 1", n)
	}
}

func TestUpsertSensorsStoresCoordinatesInBulgaria(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := s.UpsertSensors(ctx, []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	var lon, lat float64
	err := pool.QueryRow(ctx,
		`SELECT ST_X(location::geometry), ST_Y(location::geometry) FROM sensor WHERE sensor_id = 1`).
		Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if lon < 22 || lon > 29 || lat < 41 || lat > 45 {
		t.Errorf("stored (%v, %v) is outside Bulgaria — coordinates swapped", lon, lat)
	}
}

func TestWriteReadingsPersistsFlags(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(2, "P1", 900, quality.FlagSpatialOutlier, ts),
	}
	if err := s.UpsertSensors(ctx, scored, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	n, err := s.WriteReadings(ctx, scored)
	if err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d rows, want 2", n)
	}

	var flag string
	err = pool.QueryRow(ctx,
		`SELECT quality::text FROM reading WHERE sensor_id = 2`).Scan(&flag)
	if err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if flag != "spatial_outlier" {
		t.Errorf("flag = %q, want %q — bad readings must be stored, not dropped", flag, "spatial_outlier")
	}
}

func TestWriteReadingsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}

	if err := s.UpsertSensors(ctx, scored, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.WriteReadings(ctx, scored); err != nil {
			t.Fatalf("WriteReadings run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading count = %d, want 1", n)
	}
}

func TestUpsertStationsAssignsStableIDs(t *testing.T) {
	ctx, pool, s := newStore(t)

	sts := []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_06001_100",
		Code:      "BG0070A", Name: "Пловдив Каменица",
		Type: "background", Area: "urban",
		Lon: 24.75, Lat: 42.14, LastSeen: time.Now().UTC().Truncate(time.Hour),
	}}

	first, err := s.UpsertStations(ctx, sts)
	if err != nil {
		t.Fatal(err)
	}
	id := first["BG/SPO-BG0070A_06001_100"]
	if id < 9_000_000_000 {
		t.Errorf("assigned id %d is outside the reserved official range", id)
	}

	// A second pass must reuse the id, not mint a new one. Without this the
	// station's whole reading history detaches on every collector cycle.
	second, err := s.UpsertStations(ctx, sts)
	if err != nil {
		t.Fatal(err)
	}
	if second["BG/SPO-BG0070A_06001_100"] != id {
		t.Errorf("second upsert assigned %d, want %d", second["BG/SPO-BG0070A_06001_100"], id)
	}

	var source, code string
	if err := pool.QueryRow(ctx,
		`SELECT source, station_code FROM sensor WHERE sensor_id = $1`, id).Scan(&source, &code); err != nil {
		t.Fatal(err)
	}
	if source != "eea" {
		t.Errorf("source = %q, want eea", source)
	}
	if code != "BG0070A" {
		t.Errorf("station_code = %q, want BG0070A", code)
	}
}

func TestWriteStationReadingsUpsertsOnRerun(t *testing.T) {
	ctx, pool, s := newStore(t)

	ids, err := s.UpsertStations(ctx, []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_00005_100", Code: "BG0070A", Name: "x",
		Lon: 24.75, Lat: 42.14, LastSeen: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["BG/SPO-BG0070A_00005_100"]
	at := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)

	if _, err := s.WriteStationReadings(ctx, []store.StationReading{
		{SensorID: id, Metric: "P1", Value: 31.5, Timestamp: at, Quality: "ok"},
	}); err != nil {
		t.Fatal(err)
	}
	// The same hour re-fetched with a corrected value must overwrite, not
	// duplicate: the agency revises its own near-real-time data.
	if _, err := s.WriteStationReadings(ctx, []store.StationReading{
		{SensorID: id, Metric: "P1", Value: 33.0, Timestamp: at, Quality: "ok"},
	}); err != nil {
		t.Fatal(err)
	}

	var n int
	var v float64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(value) FROM reading WHERE sensor_id = $1 AND metric = 'P1'`, id).Scan(&n, &v); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("got %d rows, want 1", n)
	}
	if v != 33.0 {
		t.Errorf("value = %v, want 33", v)
	}
}

// Task 7 added Source and the four Station* fields to SensorReading and wrote
// them for an EEA station, but nothing asserted they come back POPULATED on a
// read — only that dropping the COALESCE broke an unrelated NULL scan. This is
// that missing round trip: write an EEA station and a reading through the
// store, then read it back via LatestSensors, the same path build.go's
// sensorPayloadFrom consumes.
func TestLatestSensorsCarriesEEASourceAndStationFields(t *testing.T) {
	ctx, _, s := newStore(t)

	ids, err := s.UpsertStations(ctx, []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_06001_100",
		Code:      "BG0070A", Name: "Пловдив Каменица",
		Type: "background", Area: "urban",
		Lon: 24.75, Lat: 42.14, LastSeen: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["BG/SPO-BG0070A_06001_100"]

	if _, err := s.WriteStationReadings(ctx, []store.StationReading{
		{SensorID: id, Metric: "P1", Value: 31.5, Timestamp: time.Now().UTC(), Quality: "ok"},
	}); err != nil {
		t.Fatal(err)
	}

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *store.SensorReading
	for i := range sensors {
		if sensors[i].SensorID == id {
			got = &sensors[i]
		}
	}
	if got == nil {
		t.Fatalf("LatestSensors did not return sensor %d", id)
	}
	if got.Source != "eea" {
		t.Errorf("Source = %q, want eea", got.Source)
	}
	if got.StationCode != "BG0070A" {
		t.Errorf("StationCode = %q, want BG0070A", got.StationCode)
	}
	if got.StationName != "Пловдив Каменица" {
		t.Errorf("StationName = %q, want Пловдив Каменица", got.StationName)
	}
	if got.StationType != "background" {
		t.Errorf("StationType = %q, want background", got.StationType)
	}
	if got.StationArea != "urban" {
		t.Errorf("StationArea = %q, want urban", got.StationArea)
	}
}

// F3: sensor.community ids and the 9e9 official range were kept apart by
// convention alone. Migration 00012's CHECK ties the range to the source, and
// Postgres evaluates a CHECK on the proposed row before ON CONFLICT arbitration,
// so a colliding community id is refused outright rather than silently merged.
func TestCommunitySensorCannotBeCreatedInTheOfficialIDRange(t *testing.T) {
	ctx, pool, s := newStore(t)

	ids, err := s.UpsertStations(ctx, []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_06001_100",
		Code:      "BG0070A", Name: "София - АИС Копитото",
		Type: "background", Area: "urban",
		Lon: 23.26, Lat: 42.63, LastSeen: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["BG/SPO-BG0070A_06001_100"]

	ts := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	err = s.UpsertSensors(ctx, []quality.Scored{sample(id, "P1", 24.3, quality.FlagOK, ts)}, nil)
	if err == nil {
		t.Fatal("UpsertSensors error = nil, want the sensor_id_matches_source CHECK to refuse a community row in the official range")
	}
	if !strings.Contains(err.Error(), "sensor_id_matches_source") {
		t.Errorf("error = %v, want it to name the sensor_id_matches_source constraint", err)
	}
	assertOfficialRowIntact(ctx, t, pool, id)
}

// The second half of F3, tested with the CHECK dropped so it cannot mask the
// thing under test: UpsertSensors omits source, source_ref and the station_*
// columns from its SET list, so without the WHERE a colliding id left one row
// still badged 'eea' with its station identity, at the citizen device's
// coordinates, merging both devices' readings under one id. The WHERE is what
// makes that a no-op on a database that predates 00012.
func TestCommunityUpsertDoesNotOverwriteAnOfficialStation(t *testing.T) {
	ctx, pool, s := newStore(t)

	ids, err := s.UpsertStations(ctx, []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_06001_100",
		Code:      "BG0070A", Name: "София - АИС Копитото",
		Type: "background", Area: "urban",
		Lon: 23.26, Lat: 42.63, LastSeen: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["BG/SPO-BG0070A_06001_100"]

	if _, err := pool.Exec(ctx, `ALTER TABLE sensor DROP CONSTRAINT sensor_id_matches_source`); err != nil {
		t.Fatalf("drop constraint: %v", err)
	}

	// sample() puts the device at 23.3327/42.6957 with sensor_type SDS011,
	// neither of which matches the station above.
	ts := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertSensors(ctx, []quality.Scored{sample(id, "P1", 24.3, quality.FlagOK, ts)}, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	assertOfficialRowIntact(ctx, t, pool, id)
}

// assertOfficialRowIntact checks the four columns a collision would have
// scrambled: the badge, the device type, the station identity and the location.
func assertOfficialRowIntact(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	var source, sensorType, name string
	var lon float64
	if err := pool.QueryRow(ctx,
		`SELECT source, sensor_type, station_name, ST_X(location::geometry)
		   FROM sensor WHERE sensor_id = $1`, id).Scan(&source, &sensorType, &name, &lon); err != nil {
		t.Fatal(err)
	}
	if source != "eea" {
		t.Errorf("source = %q, want eea", source)
	}
	if name != "София - АИС Копитото" {
		t.Errorf("station_name = %q, want the station's own name", name)
	}
	if sensorType != "eea_reference" {
		t.Errorf("sensor_type = %q, want eea_reference; the community upsert overwrote an official row", sensorType)
	}
	if lon < 23.25 || lon > 23.27 {
		t.Errorf("longitude = %v, want the station's 23.26; the community upsert moved an official row", lon)
	}
}
