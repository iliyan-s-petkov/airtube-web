package config

import (
	"strings"
	"time"
)

// Config is the resolved configuration: value types, no pointers, no defaults.
// Every field is guaranteed set, because readRaw refuses to return a schema with
// a nil leaf. That guarantee is why resolve can dereference freely and why the
// consuming packages never see an Option or a nil check.
type Config struct {
	Listen    Listen
	Timeouts  Timeouts
	Database  Database
	RateLimit RateLimit
	Cache     Cache
	Upstream  Upstream
	Store     Store
	Series    Series
	Quality   Quality
	Backfill  Backfill
	Frontend  Frontend
	Tiles     Tiles
}

type Listen struct {
	Addr              string
	MetricsAddr       string
	BaseURL           string
	MaxConns          int32
	TrustedProxyCIDRs []string
	CSP               string
	PermissionsPolicy string
}

type Timeouts struct {
	ReadHeader    time.Duration
	Read          time.Duration
	Write         time.Duration
	Idle          time.Duration
	ShutdownGrace time.Duration
}

type Database struct {
	// URL is env-only (AIRBG_DATABASE_URL). It is a credential, and the config
	// file is committed.
	URL               string
	APIConns          int32
	CollectorConns    int32
	MaxInflight       int32
	StatementTimeouts StatementTimeouts
}

type StatementTimeouts struct {
	Default  time.Duration
	Assign   time.Duration
	Operator time.Duration
	Series   time.Duration
}

type RateLimit struct {
	API        Bucket
	Series     SeriesBucket
	Enumerate  Enumerate
	ShardCount int
}

// Bucket carries no RetryAfter: a rate-limited client's Retry-After is computed
// from its own token deficit, never configured. See rawBucket.
type Bucket struct {
	PerSecond     float64
	Burst         float64
	TTL           time.Duration
	EvictInterval time.Duration
}

// SeriesBucket is the series bucket plus the admission-pressure hint. RetryAfter
// here is the 503 Retry-After of internal/api/series.go's admitQuery, not a
// rate-limit value; embedding Bucket keeps ratelimit.New's argument the plain
// bucket, so the two cannot be confused at a call site.
type SeriesBucket struct {
	Bucket
	RetryAfter time.Duration
}

type Enumerate struct {
	AreasPerWindow   int
	SensorsPerWindow int
	Window           time.Duration
	RetryAfter       time.Duration
}

type Cache struct {
	DataMaxAge   time.Duration
	ScalesMaxAge time.Duration
}

type Upstream struct {
	URL             string
	RequestTimeout  time.Duration
	PollInterval    time.Duration
	MinPollInterval time.Duration
	MaxPayloadBytes int64
}

type Store struct {
	CoverageThreshold int
	FreshnessWindow   time.Duration
}

type Series struct {
	DefaultMetric string
	DefaultWindow time.Duration
	Periods       map[string]Period
	// PeriodNames preserves file order, which is the order the UI offers them in.
	PeriodNames []string
}

type Period struct {
	Window time.Duration
	Hourly bool
	MaxAge time.Duration
}

type Quality struct {
	MinNeighbours         int
	MADScale              float64
	MADThreshold          float64
	NeighbourRadiusMetres float64
	EarthRadiusMetres     float64
	HistoryDepth          int
	// PMRatioThreshold and PMAbsoluteThreshold are the PM guard: a reading must
	// exceed BOTH — many times the neighbourhood median AND high in absolute
	// terms — before it is called an outlier.
	PMRatioThreshold    float64
	PMAbsoluteThreshold float64
	// SmoothFieldFloors is keyed by canonical metric name and holds only the
	// metrics that vary smoothly across space. Membership is meaningful: a
	// metric absent from this map has no spatial expectation at all.
	SmoothFieldFloors map[string]float64
	// Ranges is keyed by canonical metric name.
	Ranges map[string]Range
}

type Range struct {
	Min float64
	Max float64
}

type Backfill struct {
	HighRejectionFraction float64
}

type Frontend struct {
	NoDataColour       string
	MarkerStrokeColour string
	EmptyBasemapColour string
	ChartLineColour    string
	ZoomCity           int
	ZoomSensor         int
	// The national fallback view: roughly Bulgaria's centre, at a zoom that
	// fits the country. Used for the home page's map and for a visitor whose
	// location cannot be determined (internal/api/locate.go).
	DefaultZoom int
	DefaultLon  float64
	DefaultLat  float64
}

// Tiles configures the self-hosted basemap listener. All three keys or none:
// a partial setting starts a server whose map fetches from nowhere and says
// nothing about it. Validate enforces that.
type Tiles struct {
	// Addr is the third listener's address. It serves static files only — no
	// pool, no snapshot, no limiter — which is what makes it safe to expose
	// directly while the application port accepts only Cloudflare's ranges.
	Addr string
	// Dir holds bulgaria.pmtiles, style.json and glyphs/.
	Dir string
	// PublicURL is what the browser is told to fetch. One home for it: it
	// produces both the style URL handed to the map island and the origin the
	// CSP must allow, and two copies is how those two drift apart.
	PublicURL string
}

// Enabled reports whether a basemap is configured. Validate guarantees the
// three keys are all set or all empty, so testing one would do — testing all
// three keeps this honest if that guarantee is ever weakened.
func (t Tiles) Enabled() bool {
	return t.Addr != "" && t.Dir != "" && t.PublicURL != ""
}

// StyleURL is the MapLibre style document's URL, or empty when no basemap is
// configured. Empty is not a failure: the map island renders markers over
// frontend.empty_basemap_colour.
func (t Tiles) StyleURL() string {
	if !t.Enabled() {
		return ""
	}
	return strings.TrimSuffix(t.PublicURL, "/") + "/style.json"
}

// resolve dereferences every pointer in the raw schema. Safe to dereference
// unconditionally because readRaw has already guaranteed no leaf is nil.
// Database.URL is populated by LoadFile from the environment.
func resolve(r *raw) Config {
	cfg := Config{
		Listen: Listen{
			Addr:              *r.Listen.Addr,
			MetricsAddr:       *r.Listen.MetricsAddr,
			BaseURL:           *r.Listen.BaseURL,
			MaxConns:          *r.Listen.MaxConns,
			TrustedProxyCIDRs: *r.Listen.TrustedProxyCIDRs,
			CSP:               *r.Listen.CSP,
			PermissionsPolicy: *r.Listen.PermissionsPolicy,
		},
		Timeouts: Timeouts{
			ReadHeader:    r.Timeouts.ReadHeader.Std(),
			Read:          r.Timeouts.Read.Std(),
			Write:         r.Timeouts.Write.Std(),
			Idle:          r.Timeouts.Idle.Std(),
			ShutdownGrace: r.Timeouts.ShutdownGrace.Std(),
		},
		Database: Database{
			APIConns:       *r.Database.APIConns,
			CollectorConns: *r.Database.CollectorConns,
			MaxInflight:    *r.Database.MaxInflight,
			StatementTimeouts: StatementTimeouts{
				Default:  r.Database.StatementTimeouts.Default.Std(),
				Assign:   r.Database.StatementTimeouts.Assign.Std(),
				Operator: r.Database.StatementTimeouts.Operator.Std(),
				Series:   r.Database.StatementTimeouts.Series.Std(),
			},
		},
		RateLimit: RateLimit{
			API:        resolveBucket(r.RateLimit.API),
			Series:     resolveSeriesBucket(r.RateLimit.Series),
			ShardCount: *r.RateLimit.ShardCount,
			Enumerate: Enumerate{
				AreasPerWindow:   *r.RateLimit.Enumerate.AreasPerWindow,
				SensorsPerWindow: *r.RateLimit.Enumerate.SensorsPerWindow,
				Window:           r.RateLimit.Enumerate.Window.Std(),
				RetryAfter:       r.RateLimit.Enumerate.RetryAfter.Std(),
			},
		},
		Cache: Cache{
			DataMaxAge:   r.Cache.DataMaxAge.Std(),
			ScalesMaxAge: r.Cache.ScalesMaxAge.Std(),
		},
		Upstream: Upstream{
			URL:             *r.Upstream.URL,
			RequestTimeout:  r.Upstream.RequestTimeout.Std(),
			PollInterval:    r.Upstream.PollInterval.Std(),
			MinPollInterval: r.Upstream.MinPollInterval.Std(),
			MaxPayloadBytes: *r.Upstream.MaxPayloadBytes,
		},
		Store: Store{
			CoverageThreshold: *r.Store.CoverageThreshold,
			FreshnessWindow:   r.Store.FreshnessWindow.Std(),
		},
		Series: Series{
			DefaultMetric: *r.Series.DefaultMetric,
			DefaultWindow: r.Series.DefaultWindow.Std(),
			Periods:       make(map[string]Period, len(r.Series.Periods)),
		},
		Quality: Quality{
			MinNeighbours:         *r.Quality.MinNeighbours,
			MADScale:              *r.Quality.MADScale,
			MADThreshold:          *r.Quality.MADThreshold,
			NeighbourRadiusMetres: *r.Quality.NeighbourRadiusMetres,
			EarthRadiusMetres:     *r.Quality.EarthRadiusMetres,
			HistoryDepth:          *r.Quality.HistoryDepth,
			PMRatioThreshold:      *r.Quality.PMRatioThreshold,
			PMAbsoluteThreshold:   *r.Quality.PMAbsoluteThreshold,
			SmoothFieldFloors: map[string]float64{
				"temperature": *r.Quality.SmoothFieldFloors.Temperature,
				"humidity":    *r.Quality.SmoothFieldFloors.Humidity,
				"pressure":    *r.Quality.SmoothFieldFloors.Pressure,
			},
			Ranges: map[string]Range{
				"P1":           resolveRange(r.Quality.Ranges.P1),
				"P2":           resolveRange(r.Quality.Ranges.P2),
				"temperature":  resolveRange(r.Quality.Ranges.Temperature),
				"humidity":     resolveRange(r.Quality.Ranges.Humidity),
				"pressure":     resolveRange(r.Quality.Ranges.Pressure),
				"noise_LAeq":   resolveRange(r.Quality.Ranges.NoiseLAeq),
				"noise_LA_max": resolveRange(r.Quality.Ranges.NoiseLAMax),
			},
		},
		Backfill: Backfill{
			HighRejectionFraction: *r.Backfill.HighRejectionFraction,
		},
		Frontend: Frontend{
			NoDataColour:       *r.Frontend.NoDataColour,
			MarkerStrokeColour: *r.Frontend.MarkerStrokeColour,
			EmptyBasemapColour: *r.Frontend.EmptyBasemapColour,
			ChartLineColour:    *r.Frontend.ChartLineColour,
			ZoomCity:           *r.Frontend.ZoomCity,
			ZoomSensor:         *r.Frontend.ZoomSensor,
			DefaultZoom:        *r.Frontend.DefaultZoom,
			DefaultLon:         *r.Frontend.DefaultLon,
			DefaultLat:         *r.Frontend.DefaultLat,
		},
		Tiles: Tiles{
			Addr:      *r.Tiles.Addr,
			Dir:       *r.Tiles.Dir,
			PublicURL: *r.Tiles.PublicURL,
		},
	}

	// Resolve periods: iterate in file order and build both the map and the
	// ordered list.
	for _, p := range r.Series.Periods {
		cfg.Series.Periods[*p.Name] = Period{
			Window: p.Window.Std(),
			Hourly: *p.Hourly,
			MaxAge: p.MaxAge.Std(),
		}
		cfg.Series.PeriodNames = append(cfg.Series.PeriodNames, *p.Name)
	}

	return cfg
}

func resolveBucket(b *rawBucket) Bucket {
	return Bucket{
		PerSecond:     *b.PerSecond,
		Burst:         *b.Burst,
		TTL:           b.TTL.Std(),
		EvictInterval: b.EvictInterval.Std(),
	}
}

func resolveSeriesBucket(b *rawSeriesBucket) SeriesBucket {
	return SeriesBucket{
		Bucket: Bucket{
			PerSecond:     *b.PerSecond,
			Burst:         *b.Burst,
			TTL:           b.TTL.Std(),
			EvictInterval: b.EvictInterval.Std(),
		},
		RetryAfter: b.RetryAfter.Std(),
	}
}

func resolveRange(r *rawRange) Range {
	return Range{Min: *r.Min, Max: *r.Max}
}
