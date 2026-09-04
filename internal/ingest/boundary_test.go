package ingest_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// noBoundaryIngester is like newIngester (ingest_test.go) but deliberately
// skips importing the national boundary fixture, for tests that exercise
// the fail-closed absent-boundary path (task 17).
func noBoundaryIngester(t *testing.T, f ingest.Fetcher) (context.Context, *store.Store, *ingest.Ingester) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st := store.New(pool, testStoreConfig(), testSeriesTimeout)
	return ctx, st, ingest.New(f, st, quality.NewHistory(12), testScorer(), testAssignTimeout, testCountries)
}

// TestRunOnceRejectsSensor48524 is task-17's mandatory regression test at the
// ingest level: sensor 48524 reports country "BG" with London coordinates
// (roughly lon -0.1276, lat 51.5074), a real case observed in Task 14's live
// contract check. Before this task, RunOnce trusted upstream's Reading
// wholesale and would have scored and stored it like any other Bulgarian
// sensor. This test asserts it is written nowhere.
func TestRunOnceRejectsSensor48524(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		{
			SensorID:   48524,
			SensorType: "SDS011",
			Lon:        -0.1276,
			Lat:        51.5074,
			Metric:     "P1",
			Value:      15,
			Timestamp:  ts,
		},
	}}
	ctx, st, ing := newIngester(t, f)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Written = %d, want 0 — sensor 48524's London coordinates must be rejected regardless of its self-reported country", stats.Written)
	}

	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM reading WHERE sensor_id = 48524`).Scan(&n); err != nil {
		t.Fatalf("count readings: %v", err)
	}
	if n != 0 {
		t.Errorf("readings stored for sensor 48524 = %d, want 0", n)
	}

	var sensors int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM sensor WHERE sensor_id = 48524`).Scan(&sensors); err != nil {
		t.Fatalf("count sensor rows: %v", err)
	}
	if sensors != 0 {
		t.Errorf("sensor rows for 48524 = %d, want 0 — a foreign sensor must never reach UpsertSensors either", sensors)
	}
}

// TestRunOnceAcceptsBulgarianSensorWithBoundaryImported proves the filter
// does not collaterally reject legitimate sensors once the national boundary
// exists.
func TestRunOnceAcceptsBulgarianSensorWithBoundaryImported(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "temperature", 20, 0, ts),
	}}
	ctx, _, ing := newIngester(t, f)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 1 {
		t.Errorf("Written = %d, want 1", stats.Written)
	}
}

// recordingHandlerBoundary captures slog records so the fail-closed
// behaviour's log visibility can be asserted directly, without scraping
// stderr. A local type (rather than reusing backlog_test.go's
// recordingHandler) to keep this file independent of that one's internals.
type recordingHandlerBoundary struct {
	records []slog.Record
}

func (h *recordingHandlerBoundary) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandlerBoundary) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandlerBoundary) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandlerBoundary) WithGroup(string) slog.Handler      { return h }

// TestRunOnceFailsClosedWhenBoundaryAbsent is task-17's other mandatory
// case: on a fresh database (or one where `import-areas ... country` was
// simply never run), RunOnce must not silently ingest unfiltered — and
// therefore possibly foreign — sensors. It must instead store nothing this
// cycle and say so loudly at ERROR level, so the condition cannot go
// unnoticed the way a fail-open default would.
func TestRunOnceFailsClosedWhenBoundaryAbsent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "temperature", 20, 0, ts),
	}}
	// noBoundaryIngester migrates the schema but never imports any boundary,
	// national or otherwise.
	ctx, st, ing := noBoundaryIngester(t, f)

	handler := &recordingHandlerBoundary{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prev)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Written = %d, want 0 — with no national boundary imported, the fail-closed policy must ingest nothing", stats.Written)
	}

	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count sensors: %v", err)
	}
	if n != 0 {
		t.Errorf("sensor rows = %d, want 0", n)
	}

	found := false
	for _, rec := range handler.records {
		if rec.Level != slog.LevelError {
			continue
		}
		// The message itself must name the remedy — an operator seeing only
		// this ERROR line (everything else, including the backlog alert,
		// looks like a healthy idle system) must be able to act on it
		// without reading source code or a comment to find the fix command.
		if !strings.Contains(rec.Message, "airbg import-areas") || !strings.Contains(rec.Message, "country") {
			t.Errorf("ERROR message = %q, want it to name the remedy command (airbg import-areas <path.geojson> country)", rec.Message)
		}
		found = true
	}
	if !found {
		t.Error("no ERROR-level log record emitted for the absent-boundary fail-closed condition — this must be loudly visible, not a silent no-op")
	}
}
