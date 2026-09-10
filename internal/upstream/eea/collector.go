package eea

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"os"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
)

// writeChunkSize bounds pgx.Batch size. A full cycle can carry ~2M readings
// once history backfills; one unbounded batch would be all-or-nothing.
const writeChunkSize = 2000

// Stats is one cycle's outcome. Each discard reason gets its own counter so a
// zero-reading cycle can be diagnosed from the log line alone.
type Stats struct {
	Files            int
	Unmodified       int
	Rows             int
	Written          int
	Unplaceable      int
	Invalid          int
	OutOfRange       int
	UnknownPollutant int
	UntrustedURL     int
}

type Collector struct {
	cfg    config.EEA
	client *Client
	store  *store.Store
	scorer *quality.Scorer
	clock  func() time.Time

	metadata      Metadata
	metadataAt    time.Time
	lastFileFetch map[string]time.Time
}

// NewCollector takes the scorer rather than building one so the official layer
// is plausibility-checked by the same quality.Scorer the community ingest and
// the backfill use; scorer.InRange rejects a metric with no quality.ranges
// entry, so every canonical metric must be ranged in airbg.yaml.
func NewCollector(cfg config.EEA, s *store.Store, scorer *quality.Scorer) *Collector {
	return &Collector{
		cfg:           cfg,
		client:        New(cfg),
		store:         s,
		scorer:        scorer,
		clock:         time.Now,
		lastFileFetch: map[string]time.Time{},
	}
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
		if cached, readErr := os.ReadFile(metadataCachePath(c.cfg.MetadataCache)); readErr == nil {
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

	urls, rejected, err := c.client.FileURLs(ctx)
	if err != nil {
		return st, err
	}
	st.UntrustedURL = rejected
	if rejected > 0 {
		slog.Warn("eea file urls: rejected off-host URLs", "count", rejected)
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
		// Validity > 0 is EEA's own usable flag: provenance, not plausibility.
		// It is tested first and can only make a row worse — a row the agency
		// flags invalid stays invalid whatever the scorer says — and an
		// agency-valid row still has to pass quality.Scorer.InRange, so a
		// scale misdecode or an upstream unit change cannot be averaged in as
		// "ok". Anything but "ok" is excluded by store.usableQuality, so the
		// hour reads as a gap.
		flag := string(quality.FlagOK)
		switch {
		case k.row.Validity <= 0:
			flag = "source_invalid"
			st.Invalid++
		case math.IsNaN(value) || math.IsInf(value, 0) || !c.scorer.InRange(metric, value):
			flag = string(quality.FlagOutOfRange)
			st.OutOfRange++
		}
		readings = append(readings, store.StationReading{
			SensorID:  ids[k.row.Samplingpoint],
			Metric:    metric,
			Value:     value,
			Timestamp: k.row.Start.UTC(),
			Quality:   flag,
		})
	}

	// Chunked so one cycle's worth of readings (up to ~2M on first
	// production run) never queues into a single all-or-nothing pgx.Batch.
	// Each chunk is its own round trip: a failure part-way through leaves
	// earlier chunks durably written rather than rolling the cycle back.
	for start := 0; start < len(readings); start += writeChunkSize {
		end := start + writeChunkSize
		if end > len(readings) {
			end = len(readings)
		}
		n, err := c.store.WriteStationReadings(ctx, readings[start:end])
		st.Written += int(n)
		if err != nil {
			return st, err
		}
	}
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
			"invalid", s.Invalid, "out_of_range", s.OutOfRange,
			"unknown_pollutant", s.UnknownPollutant,
			"untrusted_url", s.UntrustedURL)
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
