package eea

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/store"
)

// Stats is one cycle's outcome. Each discard reason gets its own counter so a
// zero-reading cycle can be diagnosed from the log line alone.
type Stats struct {
	Files            int
	Unmodified       int
	Rows             int
	Written          int
	Unplaceable      int
	Invalid          int
	UnknownPollutant int
}

type Collector struct {
	cfg    config.EEA
	client *Client
	store  *store.Store
	clock  func() time.Time

	metadata      Metadata
	metadataAt    time.Time
	lastFileFetch map[string]time.Time
}

func NewCollector(cfg config.EEA, s *store.Store) *Collector {
	return &Collector{
		cfg:           cfg,
		client:        New(cfg),
		store:         s,
		clock:         time.Now,
		lastFileFetch: map[string]time.Time{},
	}
}

func (c *Collector) SetClockForTesting(clock func() time.Time) { c.clock = clock }

// metadataPath caches the 26 MB coordinate file between restarts. Upstream has
// not changed it since 2024-03-11.
func (c *Collector) metadataPath() string {
	return filepath.Join(c.cfg.MetadataCache, "PanEuropean_metadata.csv")
}

func (c *Collector) loadMetadata(ctx context.Context) error {
	now := c.clock().UTC()
	if c.metadata != nil && now.Sub(c.metadataAt) < c.cfg.MetadataInterval {
		return nil
	}

	md, err := c.client.FetchMetadata(ctx)
	if err != nil {
		if c.metadata != nil {
			// Station coordinates are static, so a stale copy is preferable to
			// an empty official layer.
			slog.Warn("eea metadata refresh failed, keeping the cached copy", "error", err)
			return nil
		}
		if cached, readErr := os.ReadFile(c.metadataPath()); readErr == nil {
			md, err = ParseMetadata(bytes.NewReader(cached), c.cfg.Countries)
			if err != nil {
				return err
			}
			c.metadata, c.metadataAt = md, now
			return nil
		}
		return err
	}

	c.metadata, c.metadataAt = md, now
	return nil
}

// RunOnce fetches, decodes and stores one pass over the configured countries.
func (c *Collector) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats

	if err := c.loadMetadata(ctx); err != nil {
		return st, err
	}

	urls, err := c.client.FileURLs(ctx)
	if err != nil {
		return st, err
	}

	stations := map[string]Station{}
	var rows []Row

	for _, u := range urls {
		st.Files++
		body, modified, err := c.client.FetchFile(ctx, u, c.lastFileFetch[u])
		if err != nil {
			slog.Warn("eea file fetch failed", "url", u, "error", err)
			continue
		}
		if !modified {
			st.Unmodified++
			continue
		}
		c.lastFileFetch[u] = c.clock().UTC()

		decoded, err := DecodeRows(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			slog.Warn("eea file decode failed", "url", u, "error", err)
			continue
		}
		rows = append(rows, decoded...)
	}
	st.Rows = len(rows)

	unplaceable := map[string]bool{}
	type keyed struct {
		row     Row
		station Station
	}
	usable := make([]keyed, 0, len(rows))

	for _, r := range rows {
		station, ok := c.metadata.Lookup(r.Samplingpoint)
		if !ok {
			unplaceable[r.Samplingpoint] = true
			continue
		}
		if _, ok := MetricFor(r.Pollutant); !ok {
			st.UnknownPollutant++
			continue
		}
		stations[r.Samplingpoint] = station
		usable = append(usable, keyed{row: r, station: station})
	}
	st.Unplaceable = len(unplaceable)
	for sp := range unplaceable {
		slog.Warn("eea sampling point has no coordinates in the metadata file", "sampling_point", sp)
	}

	if len(stations) == 0 {
		return st, nil
	}

	upserts := make([]store.StationUpsert, 0, len(stations))
	for sp, s := range stations {
		// The metadata file has no human-readable name column; the Bulgarian
		// name comes from a separate committed join table. See README.md.
		s = applyBGStationName(s)
		upserts = append(upserts, store.StationUpsert{
			SourceRef: sp,
			Code:      s.Code,
			Name:      s.Name,
			Type:      s.Type,
			Area:      s.Area,
			Lon:       s.Lon,
			Lat:       s.Lat,
			LastSeen:  c.clock().UTC(),
		})
	}
	ids, err := c.store.UpsertStations(ctx, upserts)
	if err != nil {
		return st, err
	}

	readings := make([]store.StationReading, 0, len(usable))
	for _, k := range usable {
		metric, _ := MetricFor(k.row.Pollutant)
		value, err := NormaliseValue(metric, k.row.Value, k.row.Unit)
		if err != nil {
			slog.Warn("eea reading has an unusable unit", "sampling_point", k.row.Samplingpoint, "error", err)
			continue
		}
		// Validity > 0 is EEA's own usable flag. Anything else is stored with a
		// quality that usableQuality excludes, so the hour reads as a gap.
		quality := "ok"
		if k.row.Validity <= 0 {
			quality = "source_invalid"
			st.Invalid++
		}
		readings = append(readings, store.StationReading{
			SensorID:  ids[k.row.Samplingpoint],
			Metric:    metric,
			Value:     value,
			Timestamp: k.row.Start.UTC(),
			Quality:   quality,
		})
	}

	n, err := c.store.WriteStationReadings(ctx, readings)
	if err != nil {
		return st, err
	}
	st.Written = int(n)
	return st, nil
}

// Loop runs RunOnce on the configured interval until ctx is done. A failed
// cycle is logged and the loop continues; the layer falls back to the last
// stored readings.
func (c *Collector) Loop(ctx context.Context) {
	run := func() {
		s, err := c.RunOnce(ctx)
		if err != nil {
			slog.Error("eea cycle failed", "error", err)
			return
		}
		slog.Info("eea cycle complete",
			"files", s.Files, "unmodified", s.Unmodified, "rows", s.Rows,
			"written", s.Written, "unplaceable", s.Unplaceable,
			"invalid", s.Invalid, "unknown_pollutant", s.UnknownPollutant)
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
