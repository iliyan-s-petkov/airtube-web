package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// WindForecast is one hex's forecast at one hour, as written.
//
// Declared here rather than taking internal/wind's type: that package fetches,
// and it needs the store to write what it fetched. One of the two directions
// has to be the dependency, and a persistence layer that imports its callers
// is the one that becomes a cycle.
type WindForecast struct {
	Q, R      int
	ValidAt   time.Time
	SpeedMS   float64
	Direction float64
}

// WindVector is one hex's forecast as the API serves it.
type WindVector struct {
	Q, R      int
	SpeedMS   float64
	Direction float64
}

// WriteForecasts upserts a model run. A re-fetch of the same hours replaces
// them: a later run of the same model is a correction, not a second opinion.
func (s *Store) WriteForecasts(ctx context.Context, fs []WindForecast, resolutionKM float64, model string, fetchedAt time.Time) (int64, error) {
	batch := &pgx.Batch{}
	for _, f := range fs {
		batch.Queue(
			`INSERT INTO wind_forecast (valid_at, hex_q, hex_r, resolution_km, speed_ms, direction_deg, model, fetched_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (hex_q, hex_r, valid_at) DO UPDATE
			   SET resolution_km = EXCLUDED.resolution_km,
			       speed_ms = EXCLUDED.speed_ms,
			       direction_deg = EXCLUDED.direction_deg,
			       model = EXCLUDED.model,
			       fetched_at = EXCLUDED.fetched_at`,
			f.ValidAt, f.Q, f.R, resolutionKM, f.SpeedMS, f.Direction, model, fetchedAt)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(fs)), nil
}

// CurrentWind returns the forecast hour covering now, for the given grid
// resolution.
//
// Rows written at another resolution are excluded rather than converted: their
// hex coordinates name cells of a different size, so reading them would place
// vectors on the wrong part of the map. After a resolution change the overlay
// is empty until the next fetch, which is the honest state.
func (s *Store) CurrentWind(ctx context.Context, now time.Time, resolutionKM float64) ([]WindVector, time.Time, string, error) {
	hour := TruncateHour(now)
	rows, err := s.pool.Query(ctx,
		`SELECT hex_q, hex_r, speed_ms, direction_deg, model
		   FROM wind_forecast
		  WHERE valid_at = $1 AND resolution_km = $2
		  ORDER BY hex_q, hex_r`,
		hour, resolutionKM)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	defer rows.Close()

	var out []WindVector
	var model string
	for rows.Next() {
		var v WindVector
		if err := rows.Scan(&v.Q, &v.R, &v.SpeedMS, &v.Direction, &model); err != nil {
			return nil, time.Time{}, "", err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, "", err
	}
	return out, hour, model, nil
}
