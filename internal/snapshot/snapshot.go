// Package snapshot holds the precomputed API responses that the collector
// rebuilds once per ingest cycle.
//
// The type is immutable by convention: Build constructs a Snapshot, Holder
// publishes it, and nothing mutates one afterwards. That is what makes the read
// path lock-free — a reader holding a *Snapshot cannot observe a torn or
// half-updated value, because the pointer swap is atomic and the pointee never
// changes.
package snapshot

import (
	"sync/atomic"
	"time"
)

// Body is one fully prepared HTTP response body: the JSON, its gzip encoding,
// and its ETag. All three are computed once at build time.
//
// Gzipping at build time rather than per request matters more than it looks:
// /overview is requested by every visitor and its content changes at most once
// every five minutes, so compressing it per request would burn CPU recomputing
// an identical result thousands of times.
type Body struct {
	JSON []byte
	Gzip []byte
	ETag string
}

// The one series the frontend draws by default. Exported because two packages
// must agree on it: snapshot.Build precomputes exactly this combination, and
// api.handleAreaSeries serves from the snapshot for exactly this combination.
// A literal in each package would let them drift, and the symptom would be a
// silent fall-through to the database on every page view — which is the thing
// this whole change exists to prevent.
//
// DefaultSeriesWindow must equal the window api.parsePeriod derives from
// DefaultSeriesPeriod. TestDefaultSeriesPeriodMatchesParsePeriod pins that.
const (
	DefaultSeriesMetric = "P2"
	DefaultSeriesPeriod = "24h"
	DefaultSeriesWindow = 24 * time.Hour
)

// SeriesPayload is the wire shape of both series endpoints.
//
// Columnar because uPlot consumes parallel arrays directly and same-typed
// adjacent values compress well. It lives here, rather than in the api package
// where it started, because the snapshot must produce byte-identical responses
// to the database-backed path: api can import snapshot, snapshot cannot import
// api, and two structs with matching tags would be a shape that has to be kept
// identical by discipline instead of by the compiler.
type SeriesPayload struct {
	SensorID *int64      `json:"sensor_id,omitempty"`
	Slug     string      `json:"slug,omitempty"`
	Metric   string      `json:"metric"`
	Period   string      `json:"period"`
	Hourly   bool        `json:"hourly"`
	Times    []time.Time `json:"t"`
	Values   []float64   `json:"v"`
}

// AreaMeta is the non-payload metadata a handler needs about an area: enough to
// validate a slug, resolve /locate, and render a page header, without going to
// the database.
type AreaMeta struct {
	Slug        string
	Kind        string
	NameBG      string
	NameEN      string
	CentroidLon float64
	CentroidLat float64
	DefaultZoom int
	Covered     bool
	SensorCount int
}

type Snapshot struct {
	GeneratedAt time.Time

	// Overview is the country tier (oblast aggregates). OverviewCity is the
	// regional tier (city and neighbourhood aggregates).
	Overview     Body
	OverviewCity Body
	Areas        Body

	// AreaSensors is keyed by area slug. Present for every known slug, even
	// one with no sensors — a missing key must mean "no such area" (404) and
	// never "this area happens to be empty" (200 with an empty list).
	AreaSensors map[string]Body

	// AreaSeries is the DefaultSeriesMetric / DefaultSeriesPeriod history for
	// each area, keyed by slug. Present for every known slug, with empty arrays
	// where an area has no readings — same rule as AreaSensors, for the same
	// reason: a missing key must mean 404, not "quiet area".
	//
	// Only the default combination is precomputed. Every other metric and
	// period stays database-backed on purpose: precomputing them means a
	// payload per area per metric per period, which is a cache larger than the
	// data.
	AreaSeries map[string]Body

	// KnownSlugs is the validation set for {slug} path parameters. Validating
	// against it means no caller-supplied slug ever reaches a query.
	KnownSlugs map[string]AreaMeta
}

// Holder publishes snapshots to concurrent readers.
type Holder struct {
	ptr atomic.Pointer[Snapshot]
}

func NewHolder() *Holder { return &Holder{} }

// Load returns the current snapshot, or nil if none has been built yet.
// Callers must treat nil as "not ready" and answer 503 — never as an empty
// dataset.
func (h *Holder) Load() *Snapshot { return h.ptr.Load() }

func (h *Holder) Store(s *Snapshot) { h.ptr.Store(s) }
