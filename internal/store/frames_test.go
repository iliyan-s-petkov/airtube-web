package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

func TestFrameReadingsReturnsOneRowPerHourWithItsPosition(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 12, "ok", now)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-2*time.Hour), 10, 6)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 30, 6)

	got, err := s.FrameReadings(ctx, "P2", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("FrameReadings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("readings = %d, want 2", len(got))
	}
	if got[0].Value != 10 || got[1].Value != 30 {
		t.Errorf("values = %v, %v; want 10, 30 in bucket order", got[0].Value, got[1].Value)
	}
	if got[0].Lon != 23.0 || got[0].Lat != 42.0 {
		t.Errorf("position = %v, %v; want 23, 42", got[0].Lon, got[0].Lat)
	}
	if !got[0].Bucket.Equal(now.Add(-2 * time.Hour)) {
		t.Errorf("bucket = %v, want %v", got[0].Bucket, now.Add(-2*time.Hour))
	}
}

// Half-open, like every other range in this package: the frame stamped `until`
// belongs to the next request, not this one, or a reader stepping through
// consecutive spans sees one hour twice.
func TestFrameReadingsExcludesTheBoundsItIsGiven(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 12, "ok", now)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-3*time.Hour), 1, 6)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-2*time.Hour), 2, 6)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 3, 6)

	got, err := s.FrameReadings(ctx, "P2", now.Add(-2*time.Hour), now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("FrameReadings: %v", err)
	}
	if len(got) != 1 || got[0].Value != 2 {
		t.Fatalf("readings = %+v, want only the 2 at -2h", got)
	}
}

// A device that has since gone silent still measured the air in the hour it
// reported. The live and windowed queries drop it — they answer what a sensor
// is saying now — and a frame that inherited that rule would rewrite history
// every time a device died.
func TestFrameReadingsKeepsASensorThatHasSinceGoneSilent(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 12, "ok", now.Add(-20*24*time.Hour))
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-5*time.Hour), 44, 6)

	got, err := s.FrameReadings(ctx, "P2", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("FrameReadings: %v", err)
	}
	if len(got) != 1 || got[0].Value != 44 {
		t.Fatalf("readings = %+v, want the silent device's hour", got)
	}
}

func TestFrameReadingsAnswersOnlyTheMetricAsked(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 12, "ok", now)
	seedHourly(t, ctx, pool, 1, "P2", now.Add(-1*time.Hour), 7, 6)
	seedHourly(t, ctx, pool, 1, "P1", now.Add(-1*time.Hour), 70, 6)

	got, err := s.FrameReadings(ctx, "P2", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("FrameReadings: %v", err)
	}
	if len(got) != 1 || got[0].Value != 7 {
		t.Fatalf("readings = %+v, want only P2", got)
	}
}
