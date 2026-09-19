package store

import (
	"context"
	"log/slog"
	"testing"
)

// recordingHandler captures slog records so a test can assert on level and
// message without depending on log output formatting.
type recordingHandler struct {
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// TestWarnAtAllAreaSeriesRowLimitFiresOnlyAtCap is the killing test for
// fix-round-1 item 1b: hitting AllAreaSeriesRowLimit must be logged, not
// silent, and a count merely close to the cap must not false-positive.
func TestWarnAtAllAreaSeriesRowLimitFiresOnlyAtCap(t *testing.T) {
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })

	warnAtAllAreaSeriesRowLimit(AllAreaSeriesRowLimit-1, "P2")
	if len(h.records) != 0 {
		t.Fatalf("got %d warn records for a count under the limit, want 0", len(h.records))
	}

	warnAtAllAreaSeriesRowLimit(AllAreaSeriesRowLimit, "P2")
	if len(h.records) != 1 {
		t.Fatalf("got %d warn records for a count at the limit, want 1", len(h.records))
	}
	if h.records[0].Level != slog.LevelWarn {
		t.Errorf("level = %v, want Warn", h.records[0].Level)
	}
}
