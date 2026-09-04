package ingest_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/upstream"
)

// recordingHandlerVisibility captures slog records. Like
// recordingHandlerBoundary, it is local to this file so these tests do not
// depend on another test file's internals.
type recordingHandlerVisibility struct {
	records []slog.Record
}

func (h *recordingHandlerVisibility) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandlerVisibility) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandlerVisibility) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandlerVisibility) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandlerVisibility) at(level slog.Level) []slog.Record {
	var out []slog.Record
	for _, rec := range h.records {
		if rec.Level == level {
			out = append(out, rec)
		}
	}
	return out
}

func (h *recordingHandlerVisibility) messages() []string {
	out := make([]string, 0, len(h.records))
	for _, rec := range h.records {
		out = append(out, rec.Message)
	}
	return out
}

// attr returns the value of a named attribute on a record, and whether it was
// present. Attrs are only reachable through Record.Attrs, so this is the only
// way to assert that a count actually reached the log line rather than being
// dropped on the way.
func attr(rec slog.Record, key string) (slog.Value, bool) {
	var v slog.Value
	found := false
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, found = a.Value, true
			return false
		}
		return true
	})
	return v, found
}

func capture(t *testing.T) *recordingHandlerVisibility {
	t.Helper()
	handler := &recordingHandlerVisibility{}
	prev := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return handler
}

// TestRunOnceReportsTotalUpstreamBreakAtError is the regression test for the
// silent total upstream break. Normalise drops a structurally drifted entry and
// counts it; Fetch discarded that count, and RunOnce returned before its
// cycle-complete log whenever there were no readings. So "upstream renamed a
// field and every single entry is now unusable" produced: no error, no non-zero
// exit, no stored rows, and not one log line — indistinguishable from a quiet
// night, forever.
func TestRunOnceReportsTotalUpstreamBreakAtError(t *testing.T) {
	// A successful fetch of a non-empty payload from which nothing survived.
	f := stubFetcher{skipped: 5000}
	ctx, _, ing := newIngester(t, f)
	handler := capture(t)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Skipped != 5000 {
		t.Errorf("Stats.Skipped = %d, want 5000 — the count must reach the caller, not be discarded by Fetch", stats.Skipped)
	}

	errors := handler.at(slog.LevelError)
	if len(errors) == 0 {
		t.Fatalf("no ERROR record for a cycle that fetched 5000 entries and salvaged none; records = %v", handler.messages())
	}
	got, ok := attr(errors[0], "skipped")
	if !ok || got.Int64() != 5000 {
		t.Errorf("ERROR record skipped attr = %v (present=%v), want 5000", got, ok)
	}
}

// TestRunOnceLogsCycleCompleteWithNoReadings pins the other half of the same
// defect: the cycle-complete log must be unconditional. An operator's only
// cheap liveness signal is a log line per cycle, so "no readings" must still
// produce one. Otherwise a wedged collector and a healthy idle one look the
// same in logs.
func TestRunOnceLogsCycleCompleteWithNoReadings(t *testing.T) {
	ctx, _, ing := newIngester(t, stubFetcher{})
	handler := capture(t)

	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	for _, rec := range handler.records {
		if strings.Contains(rec.Message, "ingest cycle complete") {
			return
		}
	}
	t.Errorf("no cycle-complete log record on a zero-reading cycle; records = %v", handler.messages())
}

// TestRunOnceWarnsOnPartialUpstreamBreak checks the graduated case is graduated:
// some entries unusable is a WARN, not an ERROR. If partial drift paged someone
// the signal would be ignored within a week, and the total-break ERROR above
// would be worthless.
func TestRunOnceWarnsOnPartialUpstreamBreak(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	f := stubFetcher{
		readings: []upstream.Reading{reading(1, "temperature", 20, 0, ts)},
		skipped:  3,
	}
	ctx, _, ing := newIngester(t, f)
	handler := capture(t)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Skipped != 3 {
		t.Errorf("Stats.Skipped = %d, want 3", stats.Skipped)
	}
	if errs := handler.at(slog.LevelError); len(errs) != 0 {
		t.Errorf("ERROR records on a partially usable batch = %v, want none", errs[0].Message)
	}
	found := false
	for _, rec := range handler.at(slog.LevelWarn) {
		if strings.Contains(rec.Message, "unusable") {
			found = true
		}
	}
	if !found {
		t.Errorf("no WARN record naming unusable entries; records = %v", handler.messages())
	}
}

// TestRunOnceEscalatesWhenBoundaryRejectsEverySensor is the regression test for
// the severity inversion. A boundary that exists but covers nothing has exactly
// the same operational outcome as one that was never imported — nothing is
// stored — yet it was reported at WARN while the absent-boundary case got an
// ERROR naming the remedy. The worse-diagnosed condition must not be the
// quieter one.
//
// The boundary here is a valid, non-empty polygon in the wrong place (a box in
// the Atlantic), which is the residual case Import's ST_IsValid/ST_IsEmpty
// checks cannot catch.
func TestRunOnceEscalatesWhenBoundaryRejectsEverySensor(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "temperature", 20, 0, ts),
	}}
	ctx, st, ing := noBoundaryIngester(t, f)

	if _, err := st.Pool().Exec(ctx,
		// country_code 'BG' so the boundary is in the allow list these tests
		// ingest under: the case being exercised is a boundary that is present
		// and enabled but geometrically useless, not one the filter skips.
		`INSERT INTO area (slug, kind, name_bg, name_en, country_code, geom)
		 VALUES ('misplaced', 'country', 'Грешно', 'Misplaced', 'BG',
		         ST_Multi(ST_SetSRID(ST_MakeEnvelope($1, $2, $3, $4), 4326))::geography)`,
		-30.0, 0.0, -29.0, 1.0); err != nil {
		t.Fatalf("insert misplaced boundary: %v", err)
	}

	handler := capture(t)
	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 0 {
		t.Fatalf("Written = %d, want 0 — the test premise is that every sensor is rejected", stats.Written)
	}

	errors := handler.at(slog.LevelError)
	if len(errors) == 0 {
		t.Fatalf("every sensor was rejected by a present boundary and nothing was logged at ERROR; records = %v", handler.messages())
	}
	// The message must describe what is actually wrong. The absent-boundary
	// ERROR says "not imported", which would actively mislead here — the
	// boundary is imported, it is just useless.
	msg := errors[0].Message
	if !strings.Contains(msg, "rejected by the national boundary") {
		t.Errorf("ERROR message = %q, want it to say the boundary rejected everything rather than that it is missing", msg)
	}
	if !strings.Contains(msg, "boundary geometry") {
		t.Errorf("ERROR message = %q, want it to point at the geometry as the likely cause", msg)
	}
}
