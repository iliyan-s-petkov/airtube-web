package wind

import (
	"context"
	"log/slog"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// Collector fetches the forecast for the hexes that currently hold sensors and
// stores it. See docs/wind-overlay.md.
type Collector struct {
	cfg    config.Wind
	client *Client
	store  *store.Store
	clock  func() time.Time
}

func NewCollector(cfg config.Wind, s *store.Store) *Collector {
	return &Collector{cfg: cfg, client: New(cfg), store: s, clock: time.Now}
}

func (c *Collector) SetClockForTesting(clock func() time.Time) { c.clock = clock }

// RunOnce fetches and stores one model run. Returns the number of rows written.
func (c *Collector) RunOnce(ctx context.Context) (int64, error) {
	sensors, err := c.store.LatestSensors(ctx)
	if err != nil {
		return 0, err
	}
	cells := snapshot.HexGridOf(sensors)
	if len(cells) == 0 {
		// No sensors means no grid to ask about. Not an error: it is the
		// state of a fresh database before the first ingest cycle.
		return 0, nil
	}

	points := make([]Point, len(cells))
	for i, cell := range cells {
		points[i] = Point{Q: cell.Q, R: cell.R, Lon: cell.Lon, Lat: cell.Lat}
	}

	forecasts, err := c.client.Fetch(ctx, points)
	if err != nil {
		return 0, err
	}
	rows := make([]store.WindForecast, len(forecasts))
	for i, f := range forecasts {
		rows[i] = store.WindForecast{Q: f.Q, R: f.R, ValidAt: f.ValidAt, SpeedMS: f.SpeedMS, Direction: f.Direction}
	}
	now := c.clock().UTC()
	return c.store.WriteForecasts(ctx, rows, snapshot.HexResolutionKM, c.cfg.Model, now)
}

// Loop runs RunOnce on the configured interval until ctx is done.
//
// A failed cycle is logged and the loop continues. The overlay degrades to the
// last stored forecast and then to nothing, which is the correct failure for a
// layer whose provider we do not run.
func (c *Collector) Loop(ctx context.Context) {
	run := func() {
		n, err := c.RunOnce(ctx)
		if err != nil {
			slog.Error("wind cycle failed", "error", err)
			return
		}
		slog.Info("wind cycle complete", "rows", n, "model", c.cfg.Model)
	}

	run()
	t := time.NewTicker(c.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
