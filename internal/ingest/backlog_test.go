package ingest_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// TestRunOnceDrainsRollupBacklog proves RunOnce rolls up every outstanding
// bucket from the watermark forward, not just the bucket the latest fetch
// landed in. It seeds raw readings across several past hours directly (as if
// a previous outage left them unaggregated), leaves the watermark behind
// them, then runs one ingest cycle for a *new* reading several hours later
// and asserts every intervening hour — not only the newest one — now has a
// correct hourly aggregate.
//
// Against the pre-watermark behaviour (RunOnce only ever rolling up
// store.TruncateHour(scored[0].Reading.Timestamp)) this test fails: only the
// bucket containing the new reading would be aggregated, and the earlier
// backlog buckets would come back with sample_count = 0 / "no rows".
func TestRunOnceDrainsRollupBacklog(t *testing.T) {
	ctx, st, _ := newIngester(t, nil)
	base := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	backlogHours := []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour)}

	// Seed backlog: raw readings in three past hours, written directly (as
	// if a previous, now-stopped, ingest cycle had fetched and stored them
	// but the rollup step never ran for them).
	var seed []quality.Scored
	for i, h := range backlogHours {
		seed = append(seed, quality.Scored{
			Reading: reading(1, "P1", float64(10*(i+1)), 0, h.Add(time.Minute)),
			Flag:    quality.FlagOK,
		})
	}
	if err := st.UpsertSensors(ctx, seed); err != nil {
		t.Fatalf("seed UpsertSensors: %v", err)
	}
	if _, err := st.WriteReadings(ctx, seed); err != nil {
		t.Fatalf("seed WriteReadings: %v", err)
	}

	// Leave the watermark one hour behind the backlog (bootstrap sets it to
	// exactly the given hour on a fresh database).
	if _, _, err := st.RollupBacklog(ctx, base.Add(-time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	// A new cycle's data lands three hours after the backlog started.
	current := base.Add(3 * time.Hour)
	f := stubFetcher{readings: []upstream.Reading{reading(2, "P1", 99, 0.05, current.Add(time.Minute))}}
	ing := ingest.New(f, st, quality.NewHistory(12))

	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	for i, h := range backlogHours {
		var count int
		err := st.Pool().QueryRow(ctx,
			`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
			h).Scan(&count)
		if err != nil {
			t.Fatalf("backlog bucket %d (%v) not rolled up: %v", i, h, err)
		}
		if count != 1 {
			t.Errorf("backlog bucket %d (%v): sample_count = %d, want 1", i, h, count)
		}
	}

	var currentCount int
	err := st.Pool().QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 2 AND metric = 'P1' AND bucket = $1`,
		store.TruncateHour(current)).Scan(&currentCount)
	if err != nil {
		t.Fatalf("current bucket not rolled up: %v", err)
	}
	if currentCount != 1 {
		t.Errorf("current bucket sample_count = %d, want 1", currentCount)
	}
}

// recordingHandler captures slog records so tests can assert on structured
// attributes rather than scraping formatted log text.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// findByMessage returns the attribute values (as a map) of the first record
// at the given level whose message matches, or nil if none matched.
func (h *recordingHandler) findByMessage(level slog.Level, msg string) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level != level || r.Message != msg {
			continue
		}
		attrs := map[string]any{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		return attrs
	}
	return nil
}

const backlogAlertMsg = "rollup backlog approaching raw retention boundary"

// TestBacklogAlertFiresWhenGapCrossesThreshold seeds a watermark far enough
// behind "now" that, even after RunOnce drains one tick's worth of backlog
// (capped per tick), the remaining gap still exceeds the alert threshold —
// and asserts the ERROR log carries a gap_hours attribute consistent with
// the watermark actually left behind, not merely that some log line fired.
func TestBacklogAlertFiresWhenGapCrossesThreshold(t *testing.T) {
	ctx, st, _ := newIngester(t, nil)

	// 200 hours is comfortably past the 168h threshold and comfortably
	// larger than the per-tick cap (24 buckets), so one RunOnce cannot
	// fully catch up and the alert must fire.
	current := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	staleWatermark := current.Add(-200 * time.Hour)
	if _, _, err := st.RollupBacklog(ctx, staleWatermark, 24); err != nil {
		t.Fatalf("seed stale watermark: %v", err)
	}

	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prev)

	f := stubFetcher{readings: []upstream.Reading{reading(1, "P1", 20, 0, current.Add(time.Minute))}}
	ing := ingest.New(f, st, quality.NewHistory(12))
	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	gotBucket, _, found, err := st.Watermark(ctx)
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	if !found {
		t.Fatal("watermark not set after RunOnce")
	}
	wantGap := ingest.BacklogHours(gotBucket, store.TruncateHour(current))
	if wantGap < 168 {
		t.Fatalf("test setup invalid: gap %d is not above the threshold — cap drained further than expected", wantGap)
	}

	attrs := handler.findByMessage(slog.LevelError, backlogAlertMsg)
	if attrs == nil {
		t.Fatal("expected ERROR log for backlog exceeding threshold, none found")
	}
	gotGap, ok := attrs["gap_hours"].(int64)
	if !ok {
		t.Fatalf("gap_hours attribute missing or wrong type: %#v", attrs["gap_hours"])
	}
	if gotGap != int64(wantGap) {
		t.Errorf("logged gap_hours = %d, want %d (actual watermark gap)", gotGap, wantGap)
	}
	if _, ok := attrs["watermark_bucket"]; !ok {
		t.Error("log missing watermark_bucket attribute")
	}
	if _, ok := attrs["current_bucket"]; !ok {
		t.Error("log missing current_bucket attribute")
	}
	if _, ok := attrs["margin_hours"]; !ok {
		t.Error("log missing margin_hours attribute")
	}
}

// TestBacklogAlertDoesNotFireBelowThreshold proves the alert is not emitted
// when the watermark is close enough to be fully drained within one tick's
// cap, so the resulting gap is well under the threshold.
func TestBacklogAlertDoesNotFireBelowThreshold(t *testing.T) {
	ctx, st, _ := newIngester(t, nil)

	current := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// 5 hours behind is comfortably inside the 24-bucket per-tick cap, so
	// RunOnce fully catches up to the current hour in a single cycle.
	nearWatermark := current.Add(-5 * time.Hour)
	if _, _, err := st.RollupBacklog(ctx, nearWatermark, 24); err != nil {
		t.Fatalf("seed near watermark: %v", err)
	}

	handler := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(prev)

	f := stubFetcher{readings: []upstream.Reading{reading(1, "P1", 20, 0, current.Add(time.Minute))}}
	ing := ingest.New(f, st, quality.NewHistory(12))
	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	gotBucket, _, found, err := st.Watermark(ctx)
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	if !found {
		t.Fatal("watermark not set after RunOnce")
	}
	if !gotBucket.Equal(store.TruncateHour(current)) {
		t.Fatalf("test setup invalid: watermark = %v, want fully caught up to %v", gotBucket, store.TruncateHour(current))
	}

	if attrs := handler.findByMessage(slog.LevelError, backlogAlertMsg); attrs != nil {
		t.Errorf("backlog alert fired below threshold: %#v", attrs)
	}
}
