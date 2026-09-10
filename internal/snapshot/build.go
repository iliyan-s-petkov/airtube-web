package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// countryKinds and cityKinds define the two choropleth tiers from Phase 1 §7.1.
// The country tier is oblasti only — 28 shapes, ~4 KB. The regional tier adds
// cities and Sofia's districts.
var (
	countryKinds = []string{"oblast"}
	cityKinds    = []string{"city", "neighbourhood"}
)

// areaPayload is the choropleth wire format: one entry per area, aggregate
// values only, and — deliberately — no sensor coordinates. That omission is the
// anti-extraction property from Phase 1 §7.1: the low-zoom response that every
// visitor fetches cannot be assembled into a sensor list.
type areaPayload struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Areas       []areaPayloadEntry `json:"areas"`
}

type areaPayloadEntry struct {
	Slug   string  `json:"slug"`
	Kind   string  `json:"kind"`
	NameBG string  `json:"name_bg"`
	NameEN string  `json:"name_en"`
	Lon    float64 `json:"lon"`
	Lat    float64 `json:"lat"`
	Zoom   int     `json:"zoom"`
	// Stations, not devices: one per address, matching the markers the map
	// draws for this area. The wire name is unchanged — a station is what a
	// reader calls a sensor — but the number is a count of places.
	SensorCount int                `json:"sensor_count"`
	Covered     bool               `json:"covered"`
	Values      map[string]float64 `json:"values"`
}

// sensorPayload is columnar (Phase 1 §7.3): each field named once, values in
// parallel arrays. Roughly 40 % smaller than row-per-sensor before compression,
// gzips better because same-typed values are adjacent, and it is the shape
// MapLibre's typed arrays want.
type sensorPayload struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Sensors     sensorColumns `json:"sensors"`
}

type sensorColumns struct {
	ID      []int64   `json:"id"`
	Type    []string  `json:"type"`
	Lon     []float64 `json:"lon"`
	Lat     []float64 `json:"lat"`
	Quality []string  `json:"quality"`
	// Station names the physical site each sensor stands at — see stationIDs.
	// Same length as ID, and for a sensor standing alone it is that sensor's
	// own id.
	Station []int64 `json:"station"`
	// Measures names, per sensor, the metrics that piece of hardware measures.
	// Same length as ID.
	//
	// The metric columns below cannot answer that on their own: every canonical
	// metric gets a column for every sensor, so a null in the temperature
	// column means BOTH "an SDS011 has no thermometer" and "the thermometer's
	// last reading was rejected as out of range". Those are different things to
	// tell a reader — one is a row that should not be on screen, the other is a
	// row that should say no reading right now — and this column is the only
	// thing that distinguishes them.
	Measures [][]string `json:"measures"`
	// FirstSeen and LastSeen are the device's lifetime as our ingest saw it —
	// not a registration date, which upstream does not publish. Same length as
	// ID. The panel prints them so a reader can tell a box that has reported
	// for two years from one that appeared this morning.
	FirstSeen []time.Time `json:"first_seen"`
	LastSeen  []time.Time `json:"last_seen"`
	// Metrics holds one column per canonical metric, each the same length as
	// ID. A nil entry means that sensor does not report that metric — which is
	// distinct from reporting zero, and must stay distinct: 0 µg/m³ is a
	// reading, absence is not.
	Metrics map[string][]*float64 `json:"-"`
	// Source is "sensor.community" or "eea", one per row, len(ID).
	Source []string `json:"source"`
	// EEA classification, empty strings for a sensor.community device. Each
	// len(ID).
	StationCode []string `json:"station_code"`
	StationName []string `json:"station_name"`
	StationType []string `json:"station_type"`
	StationArea []string `json:"station_area"`
}

// MarshalJSON flattens Metrics into sibling keys of the fixed columns, so the
// wire format is {"id":[…],"lon":[…],"P1":[…],"P2":[…]} rather than nesting the
// metrics under another object. Phase 1 §7.3's example payload has them as
// siblings, and Phase 3 reads them that way.
func (c sensorColumns) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"id":           c.ID,
		"type":         c.Type,
		"lon":          c.Lon,
		"lat":          c.Lat,
		"quality":      c.Quality,
		"station":      c.Station,
		"measures":     c.Measures,
		"first_seen":   c.FirstSeen,
		"last_seen":    c.LastSeen,
		"source":       c.Source,
		"station_code": c.StationCode,
		"station_name": c.StationName,
		"station_type": c.StationType,
		"station_area": c.StationArea,
	}
	for metric, col := range c.Metrics {
		out[metric] = col
	}
	return json.Marshal(out)
}

// Build reads everything the memory-backed endpoints need and prepares each
// response completely: JSON, gzip, ETag.
//
// now is passed in rather than read from the clock so a test can build twice
// with different timestamps and assert the ETag did not move.
//
// h supplies the default series combination (metric and window) rather than a
// separate config.Series argument, because the holder that will store this
// snapshot already carries it — Build and the snapshot it produces must agree
// on the same combination the holder was constructed with, and passing the
// holder itself is the only way that agreement cannot drift apart.
func Build(ctx context.Context, s *store.Store, h *Holder, now time.Time) (*Snapshot, error) {
	countryAggs, err := s.AreaAggregates(ctx, countryKinds)
	if err != nil {
		return nil, fmt.Errorf("snapshot: country tier: %w", err)
	}
	cityAggs, err := s.AreaAggregates(ctx, cityKinds)
	if err != nil {
		return nil, fmt.Errorf("snapshot: city tier: %w", err)
	}
	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot: sensors: %w", err)
	}

	// One query for every area, not one per area: Build runs on the collector
	// pool (4 connections) and the neighbourhood import multiplies the area
	// count by an order of magnitude.
	seriesBySlug, err := s.AllAreaSeries(ctx, h.metric, now.Add(-h.window), false, h.bucket)
	if err != nil {
		return nil, fmt.Errorf("snapshot: area series: %w", err)
	}

	all := make([]store.AreaAggregate, 0, len(countryAggs)+len(cityAggs))
	all = append(all, countryAggs...)
	all = append(all, cityAggs...)

	snap := &Snapshot{
		GeneratedAt: now,
		AreaSensors: make(map[string]Body, len(all)),
		AreaSeries:  make(map[string]Body, len(all)),
		KnownSlugs:  make(map[string]AreaMeta, len(all)),
	}

	for _, a := range all {
		snap.KnownSlugs[a.Slug] = AreaMeta{
			Slug: a.Slug, Kind: a.Kind, NameBG: a.NameBG, NameEN: a.NameEN,
			CentroidLon: a.CentroidLon, CentroidLat: a.CentroidLat,
			DefaultZoom: a.DefaultZoom, Covered: a.Covered, SensorCount: a.SensorCount,
			Values: a.Values,
		}
	}

	if snap.Overview, err = encode(areaPayloadFrom(now, countryAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode overview: %w", err)
	}
	if snap.OverviewCity, err = encode(areaPayloadFrom(now, cityAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode city overview: %w", err)
	}
	if snap.Areas, err = encode(areaPayloadFrom(now, all)); err != nil {
		return nil, fmt.Errorf("snapshot: encode areas: %w", err)
	}
	// The province outlines. A failure leaves Boundaries empty and logs rather
	// than failing the build: the overlay says which province you are looking
	// at, and losing it must not take the readings down with it.
	if boundaries, err := s.AreaBoundaries(ctx, countryKinds); err != nil {
		slog.Warn("snapshot: area boundaries unavailable", "error", err)
	} else if snap.Boundaries, err = encode(boundaryPayloadFrom(boundaries)); err != nil {
		return nil, fmt.Errorf("snapshot: encode boundaries: %w", err)
	}

	// Binned from the sensors already read above, not from a second fetch: the
	// grid is a different view of the same cycle's readings, and a separate
	// poller would both double the upstream load and let the two views disagree
	// about what "now" means.
	//
	// Every tier is binned from the same sensors in the same cycle, so the
	// tiers agree about the ground rather than describing two different
	// moments. They are NOT nested — hexagons do not subdivide into hexagons,
	// so a coarse bin is not the union of the fine bins under it. What makes
	// serving several resolutions no more revealing than the finest one is that
	// the finest one is published outright; see HexResolutionKM.
	snap.hexTiers = make(map[float64]hexPayload, len(HexTiersKM))
	for _, res := range HexTiersKM {
		snap.hexTiers[res] = hexPayloadFrom(now, sensors, res)
	}
	if snap.Hexes, err = encode(snap.hexTiers[HexResolutionKM]); err != nil {
		return nil, fmt.Errorf("snapshot: encode hexes: %w", err)
	}
	snap.points = pointsFrom(sensors)
	snap.SensorLocations = sensorLocationsFrom(sensors, snap.KnownSlugs)

	// The forecast overlay, read from our own table rather than fetched here:
	// the met model updates hourly and the ingest cycle runs every five
	// minutes. A failure is logged and leaves Wind empty rather than failing
	// the build — the PM map is the site, and an optional layer must not be
	// able to take it down. See docs/wind-overlay.md.
	if h.wind.Enabled {
		vectors, validAt, model, err := s.CurrentWind(ctx, now, HexResolutionKM)
		switch {
		case err != nil:
			slog.Warn("snapshot: wind unavailable", "error", err)
		case len(vectors) == 0:
			slog.Warn("snapshot: no wind forecast for the current hour", "valid_at", validAt)
		default:
			if snap.Wind, err = encode(windPayloadFrom(now, validAt, model, h.wind.ResolutionDeg, vectors)); err != nil {
				return nil, fmt.Errorf("snapshot: encode wind: %w", err)
			}
		}
	}

	// Group sensors by area. A sensor in three nested areas appears in three
	// entries; that is correct, since each is a separate response.
	bySlug := make(map[string][]store.SensorReading, len(all))
	for _, sr := range sensors {
		for _, slug := range sr.AreaSlugs {
			bySlug[slug] = append(bySlug[slug], sr)
		}
	}
	// Iterate the known areas, not bySlug, so every existing area gets an
	// entry — including empty ones. See TestBuildIncludesEmptyAreasInAreaSensors.
	for slug := range snap.KnownSlugs {
		body, err := encode(sensorPayloadFrom(now, bySlug[slug]))
		if err != nil {
			return nil, fmt.Errorf("snapshot: encode sensors for %q: %w", slug, err)
		}
		snap.AreaSensors[slug] = body

		seriesBody, err := encode(seriesPayloadFrom(slug, h.metric, seriesBySlug[slug]))
		if err != nil {
			return nil, fmt.Errorf("snapshot: encode series for %q: %w", slug, err)
		}
		snap.AreaSeries[slug] = seriesBody
	}

	// The animation history extends the snapshot currently being served, which
	// is what h holds until this build replaces it. On the first build after a
	// restart there is none, and the whole ring is read.
	if err := buildTimelapse(ctx, s, h.Load(), snap, now); err != nil {
		return nil, err
	}

	// Last, because a window is the live snapshot with some bodies replaced and
	// therefore needs the live one finished first.
	if err := buildWindows(ctx, s, snap, now); err != nil {
		return nil, err
	}

	return snap, nil
}

func areaPayloadFrom(now time.Time, aggs []store.AreaAggregate) areaPayload {
	p := areaPayload{GeneratedAt: now, Areas: make([]areaPayloadEntry, 0, len(aggs))}
	for _, a := range aggs {
		values := a.Values
		if values == nil {
			values = map[string]float64{}
		}
		p.Areas = append(p.Areas, areaPayloadEntry{
			Slug: a.Slug, Kind: a.Kind, NameBG: a.NameBG, NameEN: a.NameEN,
			Lon: a.CentroidLon, Lat: a.CentroidLat, Zoom: a.DefaultZoom,
			SensorCount: a.SensorCount, Covered: a.Covered, Values: values,
		})
	}
	return p
}

func sensorPayloadFrom(now time.Time, sensors []store.SensorReading) sensorPayload {
	n := len(sensors)
	cols := sensorColumns{
		ID:        make([]int64, 0, n),
		Type:      make([]string, 0, n),
		Lon:       make([]float64, 0, n),
		Lat:       make([]float64, 0, n),
		Quality:   make([]string, 0, n),
		Measures:  make([][]string, 0, n),
		FirstSeen: make([]time.Time, 0, n),
		LastSeen:  make([]time.Time, 0, n),
		Station:   stationIDs(sensors),
		Metrics:   make(map[string][]*float64),

		Source:      make([]string, 0, n),
		StationCode: make([]string, 0, n),
		StationName: make([]string, 0, n),
		StationType: make([]string, 0, n),
		StationArea: make([]string, 0, n),
	}
	// Every canonical metric gets a column of exactly n entries, present or
	// not. A ragged payload — where P2 has 40 entries and pressure has 3 — has
	// no way to say which sensor a value belongs to.
	metrics := upstream.CanonicalMetrics()
	for _, m := range metrics {
		cols.Metrics[m] = make([]*float64, 0, n)
	}

	for _, sr := range sensors {
		cols.ID = append(cols.ID, sr.SensorID)
		cols.Type = append(cols.Type, sr.SensorType)
		cols.Lon = append(cols.Lon, sr.Lon)
		cols.Lat = append(cols.Lat, sr.Lat)
		cols.Quality = append(cols.Quality, sr.Quality)
		cols.Measures = append(cols.Measures, measuresOf(sr, metrics))
		cols.FirstSeen = append(cols.FirstSeen, sr.FirstSeen)
		cols.LastSeen = append(cols.LastSeen, sr.LastSeen)
		cols.Source = append(cols.Source, sr.Source)
		cols.StationCode = append(cols.StationCode, sr.StationCode)
		cols.StationName = append(cols.StationName, sr.StationName)
		cols.StationType = append(cols.StationType, sr.StationType)
		cols.StationArea = append(cols.StationArea, sr.StationArea)
		for _, m := range metrics {
			if v, ok := sr.Values[m]; ok {
				value := v
				cols.Metrics[m] = append(cols.Metrics[m], &value)
			} else {
				cols.Metrics[m] = append(cols.Metrics[m], nil)
			}
		}
	}
	return sensorPayload{GeneratedAt: now, Sensors: cols}
}

// measuresOf is the metrics one device measures, in the canonical order.
//
// The store's answer is every metric with a fresh reading of any quality; this
// filters it to the canonical set (the same set the columns are built from, so
// the client can never be told about a metric it has no column for) and adds
// anything present in Values but missing from it — a usable value the device
// did not also report as measured would otherwise be a reading the panel
// refuses to show.
//
// A device the store has no Measures for at all — a row written before the
// column existed — falls back to what it has values for, which is the old
// behaviour minus the phantom rows.
func measuresOf(sr store.SensorReading, canonical []string) []string {
	reported := make(map[string]bool, len(sr.Measures))
	for _, m := range sr.Measures {
		reported[m] = true
	}
	out := make([]string, 0, len(canonical))
	for _, m := range canonical {
		if _, has := sr.Values[m]; has || reported[m] {
			out = append(out, m)
		}
	}
	return out
}

// stationIDs answers, for each sensor and in the same order, which physical
// site it stands at.
//
// Upstream publishes a station as SEVERAL sensor ids at one address: the PM
// box (SDS011, SPS30, PMS5003) and the climate box (BME280, SHT3x) are
// separate devices with separate ids and identical published coordinates, each
// reporting only the metrics its own hardware measures. Nationwide that is 259
// sensors at 151 addresses. A map of one marker per id therefore draws the
// climate box exactly underneath the PM box, where nothing can click it, and
// the PM box's panel answers "no reading" for temperature, humidity and
// pressure that are being measured a metre away.
//
// Grouped on the exact published coordinate, not on a radius. Both devices of
// a pair carry the same position to the last digit, so exact equality catches
// every real pair; a radius would additionally merge devices at genuinely
// different addresses on the same street, which is a claim about the data
// nobody made.
//
// The station's id is the SMALLEST member id, so the grouping does not depend
// on the order rows arrive in, and so the same site keeps the same id from one
// snapshot to the next for as long as that member reports.
func stationIDs(sensors []store.SensorReading) []int64 {
	type site struct{ lon, lat float64 }
	lowest := make(map[site]int64, len(sensors))
	for _, sr := range sensors {
		k := site{sr.Lon, sr.Lat}
		if id, seen := lowest[k]; !seen || sr.SensorID < id {
			lowest[k] = sr.SensorID
		}
	}
	out := make([]int64, 0, len(sensors))
	for _, sr := range sensors {
		out = append(out, lowest[site{sr.Lon, sr.Lat}])
	}
	return out
}

// seriesPayloadFrom converts store points to the wire shape.
//
// The slices are allocated with make even when there are no points: a nil slice
// marshals to `null`, and a charting library handed null throws instead of
// drawing an empty axis.
func seriesPayloadFrom(slug, metric string, points []store.Point) SeriesPayload {
	p := SeriesPayload{
		Slug:   slug,
		Metric: metric,
		Period: DefaultSeriesPeriod,
		Hourly: false,
		Times:  make([]time.Time, 0, len(points)),
		Values: make([]float64, 0, len(points)),
	}
	for _, pt := range points {
		p.Times = append(p.Times, pt.Time)
		p.Values = append(p.Values, pt.Value)
	}
	return p
}

// encode serialises, gzips, and hashes one payload.
//
// The ETag is the SHA-256 of the JSON body with GeneratedAt zeroed out first.
// Hashing the timestamped body would change the ETag every cycle even when no
// value moved, invalidating every cached copy five minutes after it was stored
// — which defeats the edge cache entirely on a dataset that changes slowly.
func encode(payload any) (Body, error) {
	withTime, err := json.Marshal(payload)
	if err != nil {
		return Body{}, err
	}

	etagSource, err := json.Marshal(zeroGeneratedAt(payload))
	if err != nil {
		return Body{}, err
	}
	sum := sha256.Sum256(etagSource)

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return Body{}, err
	}
	if _, err := zw.Write(withTime); err != nil {
		return Body{}, err
	}
	if err := zw.Close(); err != nil {
		return Body{}, err
	}

	return Body{
		JSON: withTime,
		Gzip: buf.Bytes(),
		// Quoted, as RFC 9110 requires. A bare hex string is not a valid
		// entity-tag and intermediaries are free to ignore it.
		ETag: `"` + hex.EncodeToString(sum[:]) + `"`,
	}, nil
}

// zeroGeneratedAt returns a copy of the payload with its timestamp cleared, for
// hashing only. Handled per concrete type rather than by reflection: the
// payload types are few and named here, and a reflective version would silently
// stop working the moment one is added without a matching case.
func zeroGeneratedAt(payload any) any {
	switch p := payload.(type) {
	case areaPayload:
		p.GeneratedAt = time.Time{}
		return p
	case sensorPayload:
		p.GeneratedAt = time.Time{}
		return p
	case hexPayload:
		p.GeneratedAt = time.Time{}
		return p
	default:
		// Unknown payload type: hash it as-is rather than silently returning
		// something that is not the payload. A caller adding a third type gets
		// per-cycle ETag churn, which is visible in cache metrics, rather than
		// a wrong hash.
		return payload
	}
}
