package config

// raw is the on-disk shape of airbg.yaml. Every leaf field is a pointer: this
// package has no defaults, so "the operator omitted this key" must be
// distinguishable from "the operator wrote 0". Zero is the dangerous value for
// most of these — max_conns: 0 is an unlimited public listener, and
// coverage_threshold: 0 paints a single sensor as a whole oblast.
//
// The yaml tags are load-bearing twice over: they name the file's keys, and the
// environment overlay derives AIRBG_* names from the tag path (see envName).
type raw struct {
	Listen    *rawListen    `yaml:"listen"`
	Timeouts  *rawTimeouts  `yaml:"timeouts"`
	Database  *rawDatabase  `yaml:"database"`
	RateLimit *rawRateLimit `yaml:"ratelimit"`
	Cache     *rawCache     `yaml:"cache"`
	Upstream  *rawUpstream  `yaml:"upstream"`
	Store     *rawStore     `yaml:"store"`
	Series    *rawSeries    `yaml:"series"`
	Quality   *rawQuality   `yaml:"quality"`
	Backfill  *rawBackfill  `yaml:"backfill"`
	Frontend  *rawFrontend  `yaml:"frontend"`
	Tiles     *rawTiles     `yaml:"tiles"`
}

type rawListen struct {
	Addr              *string   `yaml:"addr"`
	MetricsAddr       *string   `yaml:"metrics_addr"`
	BaseURL           *string   `yaml:"base_url"`
	MaxConns          *int32    `yaml:"max_conns"`
	TrustedProxyCIDRs *[]string `yaml:"trusted_proxy_cidrs"`
	CSP               *string   `yaml:"csp"`
	PermissionsPolicy *string   `yaml:"permissions_policy"`
}

type rawTimeouts struct {
	ReadHeader    *Duration `yaml:"read_header"`
	Read          *Duration `yaml:"read"`
	Write         *Duration `yaml:"write"`
	Idle          *Duration `yaml:"idle"`
	ShutdownGrace *Duration `yaml:"shutdown_grace"`
}

type rawDatabase struct {
	APIConns          *int32                `yaml:"api_conns"`
	CollectorConns    *int32                `yaml:"collector_conns"`
	MaxInflight       *int32                `yaml:"max_inflight"`
	StatementTimeouts *rawStatementTimeouts `yaml:"statement_timeouts"`
}

type rawStatementTimeouts struct {
	Default  *Duration `yaml:"default"`
	Assign   *Duration `yaml:"assign"`
	Operator *Duration `yaml:"operator"`
	Series   *Duration `yaml:"series"`
}

type rawRateLimit struct {
	API        *rawBucket       `yaml:"api"`
	Series     *rawSeriesBucket `yaml:"series"`
	Enumerate  *rawEnumerate    `yaml:"enumerate"`
	ShardCount *int             `yaml:"shard_count"`
}

// rawBucket deliberately has no retry_after. The 429 a token bucket produces
// carries a Retry-After computed from that client's own token deficit
// (internal/ratelimit/bucket.go, internal/httpx/chain.go), which tells the
// caller when tokens will actually be available; a static key would only ever
// be a less accurate second answer, and was verified to be read by nothing.
type rawBucket struct {
	PerSecond     *float64  `yaml:"per_second"`
	Burst         *float64  `yaml:"burst"`
	TTL           *Duration `yaml:"ttl"`
	EvictInterval *Duration `yaml:"evict_interval"`
}

// rawSeriesBucket is rawBucket plus retry_after, which is live: it is the hint
// on the 503 the series routes return when the database admission pool is full
// (internal/api/series.go's admitQuery) — an admission decision, not a
// rate-limit one, so it is not computable from any bucket's token deficit.
//
// Written out flat rather than embedding rawBucket: missingKeys and applyEnv
// walk yaml tags by reflection, and a `,inline` embedded struct has no key name
// for them to build a dotted path from.
type rawSeriesBucket struct {
	PerSecond     *float64  `yaml:"per_second"`
	Burst         *float64  `yaml:"burst"`
	TTL           *Duration `yaml:"ttl"`
	EvictInterval *Duration `yaml:"evict_interval"`
	RetryAfter    *Duration `yaml:"retry_after"`
}

type rawEnumerate struct {
	AreasPerWindow   *int      `yaml:"areas_per_window"`
	SensorsPerWindow *int      `yaml:"sensors_per_window"`
	Window           *Duration `yaml:"window"`
	RetryAfter       *Duration `yaml:"retry_after"`
}

type rawCache struct {
	DataMaxAge   *Duration `yaml:"data_max_age"`
	ScalesMaxAge *Duration `yaml:"scales_max_age"`
}

type rawUpstream struct {
	URL             *string   `yaml:"url"`
	RequestTimeout  *Duration `yaml:"request_timeout"`
	PollInterval    *Duration `yaml:"poll_interval"`
	MinPollInterval *Duration `yaml:"min_poll_interval"`
	MaxPayloadBytes *int64    `yaml:"max_payload_bytes"`
}

type rawStore struct {
	CoverageThreshold *int      `yaml:"coverage_threshold"`
	FreshnessWindow   *Duration `yaml:"freshness_window"`
}

type rawSeries struct {
	DefaultMetric *string     `yaml:"default_metric"`
	DefaultWindow *Duration   `yaml:"default_window"`
	Periods       []rawPeriod `yaml:"periods"`
}

// rawPeriod is a list entry, not a map, so that ordering is stable in the file
// and a duplicate name is detectable. Each period carries its own cache
// lifetime: internal/api/series.go's seriesMaxAge is an explicit table, not a
// formula, because four values each need their own justification.
type rawPeriod struct {
	Name   *string   `yaml:"name"`
	Window *Duration `yaml:"window"`
	Hourly *bool     `yaml:"hourly"`
	MaxAge *Duration `yaml:"max_age"`
}

type rawQuality struct {
	MinNeighbours         *int       `yaml:"min_neighbours"`
	MADScale              *float64   `yaml:"mad_scale"`
	MADThreshold          *float64   `yaml:"mad_threshold"`
	NeighbourRadiusMetres *float64   `yaml:"neighbour_radius_metres"`
	EarthRadiusMetres     *float64   `yaml:"earth_radius_metres"`
	HistoryDepth          *int       `yaml:"history_depth"`
	PMRatioThreshold      *float64   `yaml:"pm_ratio_threshold"`
	PMAbsoluteThreshold   *float64   `yaml:"pm_absolute_threshold"`
	SmoothFieldFloors     *rawFloors `yaml:"smooth_field_floors"`
	Ranges                *rawRanges `yaml:"ranges"`
}

// rawFloors is a fixed struct rather than a map[string]float64 for the same
// reason rawRanges is: the set of smooth fields is a code fact (each one has a
// spatial check written for it), so an operator adding a fourth key must get a
// strict-decode error, not a silently ignored entry.
type rawFloors struct {
	Temperature *float64 `yaml:"temperature"`
	Humidity    *float64 `yaml:"humidity"`
	Pressure    *float64 `yaml:"pressure"`
}

type rawRanges struct {
	P1          *rawRange `yaml:"P1"`
	P2          *rawRange `yaml:"P2"`
	Temperature *rawRange `yaml:"temperature"`
	Humidity    *rawRange `yaml:"humidity"`
	Pressure    *rawRange `yaml:"pressure"`
	NoiseLAeq   *rawRange `yaml:"noise_LAeq"`
	NoiseLAMax  *rawRange `yaml:"noise_LA_max"`
}

type rawRange struct {
	Min *float64 `yaml:"min"`
	Max *float64 `yaml:"max"`
}

type rawBackfill struct {
	HighRejectionFraction *float64 `yaml:"high_rejection_fraction"`
}

type rawFrontend struct {
	NoDataColour       *string `yaml:"no_data_colour"`
	UnscaledColour     *string `yaml:"unscaled_colour"`
	MarkerStrokeColour *string `yaml:"marker_stroke_colour"`
	EmptyBasemapColour *string `yaml:"empty_basemap_colour"`
	ChartLineColour    *string `yaml:"chart_line_colour"`
	ZoomCity           *int    `yaml:"zoom_city"`
	ZoomSensor         *int    `yaml:"zoom_sensor"`
	// The national fallback view. One home for it, because it is rendered into
	// the home page's map island AND returned by /api/v1/locate; two copies is
	// how the two views drift apart.
	DefaultZoom *int     `yaml:"default_zoom"`
	DefaultLon  *float64 `yaml:"default_lon"`
	DefaultLat  *float64 `yaml:"default_lat"`
}

// rawTiles has no key field, and must never grow one. The whole point of the
// self-hosted basemap is that there is no vendor to authenticate to; a key here
// would also route a credential through assignScalar's value-echoing parse
// errors, the same reason rawDatabase has no url field.
type rawTiles struct {
	Addr      *string `yaml:"addr"`
	Dir       *string `yaml:"dir"`
	PublicURL *string `yaml:"public_url"`
	Archive   *string `yaml:"archive"`
}
