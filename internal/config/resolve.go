package config

import "time"

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
	Basemap   Basemap
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
}

type Basemap struct {
	// StyleURL already has {key} substituted.
	StyleURL string
	// Key is env-only (AIRBG_BASEMAP_KEY).
	Key string
}

// resolve dereferences every pointer in the raw schema. Safe to dereference
// unconditionally because readRaw has already guaranteed no leaf is nil.
// Database.URL and Basemap.Key are populated by Task 7 (LoadFile) from
// environment variables; Basemap.StyleURL arrives with {key} already substituted.
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
		},
		Basemap: Basemap{
			StyleURL: *r.Basemap.StyleURL,
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
