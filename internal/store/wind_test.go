package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

func TestWriteForecastsReplacesAnEarlierRunOfTheSameHour(t *testing.T) {
	ctx, pool, s := newStore(t)
	hour := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

	if _, err := s.WriteForecasts(ctx,
		[]store.WindForecast{{Q: 1, R: 2, ValidAt: hour, SpeedMS: 3, Direction: 90}},
		15, "ecmwf_ifs025", hour); err != nil {
		t.Fatalf("WriteForecasts: %v", err)
	}
	// A later run of the same model is a correction, not a second opinion — two
	// rows for one hex-hour would draw two arrows on one hex.
	if _, err := s.WriteForecasts(ctx,
		[]store.WindForecast{{Q: 1, R: 2, ValidAt: hour, SpeedMS: 7, Direction: 180}},
		15, "ecmwf_ifs025", hour.Add(time.Hour)); err != nil {
		t.Fatalf("WriteForecasts (second run): %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM wind_forecast`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}

	vs, _, _, err := s.CurrentWind(ctx, hour.Add(20*time.Minute), 15)
	if err != nil {
		t.Fatalf("CurrentWind: %v", err)
	}
	if len(vs) != 1 || vs[0].SpeedMS != 7 || vs[0].Direction != 180 {
		t.Errorf("got %+v, want the second run's values", vs)
	}
}

// CurrentWind is asked for the hour containing now, not for now.
func TestCurrentWindReadsTheHourContainingNow(t *testing.T) {
	ctx, _, s := newStore(t)
	hour := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

	if _, err := s.WriteForecasts(ctx,
		[]store.WindForecast{{Q: 0, R: 0, ValidAt: hour, SpeedMS: 4, Direction: 270}},
		15, "ecmwf_ifs025", hour); err != nil {
		t.Fatalf("WriteForecasts: %v", err)
	}

	vs, validAt, model, err := s.CurrentWind(ctx, hour.Add(59*time.Minute), 15)
	if err != nil {
		t.Fatalf("CurrentWind: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("got %d vectors, want 1", len(vs))
	}
	if !validAt.Equal(hour) {
		t.Errorf("valid_at = %v, want the forecast hour %v", validAt, hour)
	}
	if model != "ecmwf_ifs025" {
		t.Errorf("model = %q, want the stored model", model)
	}

	// The next hour has no row, and an empty overlay is the honest answer —
	// serving the previous hour would silently age the forecast an hour.
	if vs, _, _, err := s.CurrentWind(ctx, hour.Add(time.Hour), 15); err != nil || len(vs) != 0 {
		t.Errorf("got %d vectors for the next hour (err %v), want 0", len(vs), err)
	}
}

// A hex coordinate names a cell of a particular size, so rows from another grid
// would place their vectors on the wrong part of the map.
func TestCurrentWindExcludesAnotherResolutionsRows(t *testing.T) {
	ctx, _, s := newStore(t)
	hour := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

	if _, err := s.WriteForecasts(ctx,
		[]store.WindForecast{{Q: 3, R: 4, ValidAt: hour, SpeedMS: 5, Direction: 45}},
		25, "ecmwf_ifs025", hour); err != nil {
		t.Fatalf("WriteForecasts: %v", err)
	}

	vs, _, _, err := s.CurrentWind(ctx, hour, 15)
	if err != nil {
		t.Fatalf("CurrentWind: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("got %d vectors from a 25 km grid, want 0 — the coordinates name different cells", len(vs))
	}
}

func TestWriteForecastsOfNothingWritesNothing(t *testing.T) {
	ctx, pool, s := newStore(t)

	n, err := s.WriteForecasts(ctx, nil, 15, "ecmwf_ifs025", time.Now().UTC())
	if err != nil || n != 0 {
		t.Fatalf("WriteForecasts(nil) = %d, %v; want 0, nil", n, err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM wind_forecast`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("row count = %d, want 0", rows)
	}
}
