package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// hostPattern and maxHostLength are validation mechanics, not tunables: they
// describe what a hostname is, which is not an operator decision.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)

const maxHostLength = 253

var colourPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var canonicalMetrics = map[string]bool{
	"P1": true, "P2": true, "temperature": true, "humidity": true,
	"pressure": true, "noise_LAeq": true, "noise_LA_max": true,
}

// problems accumulates every violation so an operator sees the whole list in one
// startup attempt rather than one per restart.
type problems []string

func (p *problems) addf(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p *problems) positive(path string, d time.Duration) {
	if d <= 0 {
		p.addf("%s = %v, must be greater than zero", path, d)
	}
}

func (p *problems) positiveInt(path string, n int) {
	if n <= 0 {
		p.addf("%s = %d, must be greater than zero", path, n)
	}
}

func (p *problems) positiveFloat(path string, x float64) {
	if x <= 0 {
		p.addf("%s = %v, must be greater than zero", path, x)
	}
}

func (c Config) Validate() error {
	var p problems

	c.validateListen(&p)
	c.validateTimeouts(&p)
	c.validateDatabase(&p)
	c.validateRateLimit(&p)
	c.validateUpstreamAndCache(&p)
	c.validateStoreAndSeries(&p)
	c.validateQuality(&p)
	c.validateFrontend(&p)

	if len(p) > 0 {
		return fmt.Errorf("config: %d problem(s):\n  %s", len(p), strings.Join(p, "\n  "))
	}
	return nil
}

func (c Config) validateListen(p *problems) {
	for path, addr := range map[string]string{
		"listen.addr":         c.Listen.Addr,
		"listen.metrics_addr": c.Listen.MetricsAddr,
	} {
		if addr == "" {
			p.addf("%s is empty", path)
			continue
		}
		if len(addr) > maxHostLength {
			p.addf("%s is %d bytes, must be at most %d", path, len(addr), maxHostLength)
		}
		if !hostPattern.MatchString(addr) {
			p.addf("%s = %q, must be host:port", path, addr)
		}
	}
	// Sharing the address means /metrics is reachable from the public chain,
	// which hands an attacker the counters that show whether their probing is
	// being rate limited.
	if c.Listen.Addr == c.Listen.MetricsAddr {
		p.addf("listen.addr and listen.metrics_addr are both %q; the private listener must be separate", c.Listen.Addr)
	}
	if u, err := url.Parse(c.Listen.BaseURL); err != nil {
		p.addf("listen.base_url = %q is not a URL: %v", c.Listen.BaseURL, err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		p.addf("listen.base_url = %q must use http or https", c.Listen.BaseURL)
	} else if u.Host == "" {
		p.addf("listen.base_url = %q must be absolute", c.Listen.BaseURL)
	} else if u.User != nil {
		p.addf("listen.base_url must not contain userinfo")
	}
	if c.Listen.MaxConns <= 0 {
		p.addf("listen.max_conns = %d, must be greater than zero; zero would be an unlimited public listener", c.Listen.MaxConns)
	}
	for _, cidr := range c.Listen.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			p.addf("listen.trusted_proxy_cidrs contains %q, which is not a CIDR: %v", cidr, err)
		}
	}
	// A CSP with either of these is decorative. Making the policy configurable
	// must not make it disableable.
	for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
		if strings.Contains(c.Listen.CSP, bad) {
			p.addf("listen.csp contains %q, which is never permitted", bad)
		}
	}
	if !strings.Contains(c.Listen.CSP, "default-src") {
		p.addf("listen.csp has no default-src directive")
	}
	if c.Listen.PermissionsPolicy == "" {
		p.addf("listen.permissions_policy is empty; write an explicit denial list instead")
	}
}

func (c Config) validateTimeouts(p *problems) {
	p.positive("timeouts.read_header", c.Timeouts.ReadHeader)
	p.positive("timeouts.read", c.Timeouts.Read)
	p.positive("timeouts.write", c.Timeouts.Write)
	p.positive("timeouts.idle", c.Timeouts.Idle)
	p.positive("timeouts.shutdown_grace", c.Timeouts.ShutdownGrace)
	if c.Timeouts.Read < c.Timeouts.ReadHeader {
		p.addf("timeouts.read (%v) is shorter than timeouts.read_header (%v)", c.Timeouts.Read, c.Timeouts.ReadHeader)
	}
}

func (c Config) validateDatabase(p *problems) {
	if c.Database.URL == "" {
		p.addf("%s is not set in the environment; it is required and must never be written to the config file", DatabaseURLEnv)
	}
	if c.Database.APIConns <= 0 {
		p.addf("database.api_conns = %d, must be greater than zero", c.Database.APIConns)
	}
	if c.Database.CollectorConns <= 0 {
		p.addf("database.collector_conns = %d, must be greater than zero", c.Database.CollectorConns)
	}
	if c.Database.MaxInflight <= 0 {
		p.addf("database.max_inflight = %d, must be greater than zero", c.Database.MaxInflight)
	}
	t := c.Database.StatementTimeouts
	p.positive("database.statement_timeouts.default", t.Default)
	p.positive("database.statement_timeouts.assign", t.Assign)
	p.positive("database.statement_timeouts.operator", t.Operator)
	p.positive("database.statement_timeouts.series", t.Series)
	// /series is the most expensive public query, so its budget must stay at or
	// below the default rather than above it.
	if t.Series > t.Default {
		p.addf("database.statement_timeouts.series (%v) exceeds .default (%v); the public series query must be the tighter budget", t.Series, t.Default)
	}
}

func (c Config) validateRateLimit(p *problems) {
	for path, b := range map[string]Bucket{
		"ratelimit.api":    c.RateLimit.API,
		"ratelimit.series": c.RateLimit.Series,
	} {
		p.positiveFloat(path+".per_second", b.PerSecond)
		p.positiveFloat(path+".burst", b.Burst)
		p.positive(path+".ttl", b.TTL)
		p.positive(path+".evict_interval", b.EvictInterval)
		p.positive(path+".retry_after", b.RetryAfter)
		if b.Burst < b.PerSecond {
			p.addf("%s.burst (%v) is below .per_second (%v); the bucket could never fill for one second of traffic", path, b.Burst, b.PerSecond)
		}
		if b.EvictInterval > b.TTL {
			p.addf("%s.evict_interval (%v) exceeds .ttl (%v); entries would outlive their bucket", path, b.EvictInterval, b.TTL)
		}
	}
	e := c.RateLimit.Enumerate
	p.positiveInt("ratelimit.enumerate.areas_per_window", e.AreasPerWindow)
	p.positiveInt("ratelimit.enumerate.sensors_per_window", e.SensorsPerWindow)
	p.positive("ratelimit.enumerate.window", e.Window)
	p.positive("ratelimit.enumerate.retry_after", e.RetryAfter)
	p.positiveInt("ratelimit.shard_count", c.RateLimit.ShardCount)
}

func (c Config) validateUpstreamAndCache(p *problems) {
	if u, err := url.Parse(c.Upstream.URL); err != nil {
		p.addf("upstream.url = %q is not a URL: %v", c.Upstream.URL, err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		p.addf("upstream.url = %q must use http or https", c.Upstream.URL)
	} else if u.Host == "" {
		p.addf("upstream.url = %q must be absolute", c.Upstream.URL)
	}
	p.positive("upstream.request_timeout", c.Upstream.RequestTimeout)
	p.positive("upstream.min_poll_interval", c.Upstream.MinPollInterval)
	// Polling a volunteer-run public API faster than the floor is abusive, so
	// the floor is enforced rather than advisory.
	if c.Upstream.PollInterval < c.Upstream.MinPollInterval {
		p.addf("upstream.poll_interval (%v) is below upstream.min_poll_interval (%v)", c.Upstream.PollInterval, c.Upstream.MinPollInterval)
	}
	if c.Upstream.MaxPayloadBytes <= 0 {
		p.addf("upstream.max_payload_bytes = %d, must be greater than zero", c.Upstream.MaxPayloadBytes)
	}
	p.positive("cache.data_max_age", c.Cache.DataMaxAge)
	p.positive("cache.scales_max_age", c.Cache.ScalesMaxAge)
	// A client caching for longer than one ingest cycle can show a reading that
	// has already been superseded. This was a code comment; here it is checked.
	if half := c.Upstream.PollInterval / 2; c.Cache.DataMaxAge > half {
		p.addf("cache.data_max_age (%v) exceeds half of upstream.poll_interval (%v)", c.Cache.DataMaxAge, half)
	}
}

func (c Config) validateStoreAndSeries(p *problems) {
	if c.Store.CoverageThreshold < 1 {
		p.addf("store.coverage_threshold = %d, must be at least 1; below that a single sensor would be painted as a whole area", c.Store.CoverageThreshold)
	}
	p.positive("store.freshness_window", c.Store.FreshnessWindow)

	if !canonicalMetrics[c.Series.DefaultMetric] {
		p.addf("series.default_metric = %q is not a canonical metric", c.Series.DefaultMetric)
	}
	p.positive("series.default_window", c.Series.DefaultWindow)
	if len(c.Series.Periods) == 0 {
		p.addf("series.periods is empty")
	}
	seen := map[string]bool{}
	for _, name := range c.Series.PeriodNames {
		if seen[name] {
			p.addf("series.periods has a duplicate entry named %q", name)
		}
		seen[name] = true
		pd := c.Series.Periods[name]
		if name == "" {
			p.addf("series.periods has an entry with an empty name")
		}
		p.positive(fmt.Sprintf("series.periods[%s].window", name), pd.Window)
		p.positive(fmt.Sprintf("series.periods[%s].max_age", name), pd.MaxAge)
	}
	// The snapshot serves the default window without touching the database, so
	// the default window must equal the window api.parsePeriod derives from one
	// of the configured periods. If it does not, the snapshot answers a question
	// no period asks.
	matched := false
	for _, pd := range c.Series.Periods {
		if pd.Window == c.Series.DefaultWindow {
			matched = true
			break
		}
	}
	if !matched && len(c.Series.Periods) > 0 {
		p.addf("series.default_window (%v) matches no entry in series.periods", c.Series.DefaultWindow)
	}
}

func (c Config) validateQuality(p *problems) {
	q := c.Quality
	if q.MinNeighbours < 1 {
		p.addf("quality.min_neighbours = %d, must be at least 1", q.MinNeighbours)
	}
	p.positiveFloat("quality.mad_scale", q.MADScale)
	p.positiveFloat("quality.mad_threshold", q.MADThreshold)
	p.positiveFloat("quality.neighbour_radius_metres", q.NeighbourRadiusMetres)
	p.positiveFloat("quality.earth_radius_metres", q.EarthRadiusMetres)
	if q.HistoryDepth < 1 {
		p.addf("quality.history_depth = %d, must be at least 1", q.HistoryDepth)
	}
	for metric := range canonicalMetrics {
		rng, ok := q.Ranges[metric]
		if !ok {
			p.addf("quality.ranges has no entry for %q; its readings would never be plausibility-checked", metric)
			continue
		}
		if rng.Max <= rng.Min {
			p.addf("quality.ranges.%s: max (%v) must exceed min (%v)", metric, rng.Max, rng.Min)
		}
	}
	f := c.Backfill.HighRejectionFraction
	if f <= 0 || f > 1 {
		p.addf("backfill.high_rejection_fraction = %v, must be in (0, 1]", f)
	}
}

func (c Config) validateFrontend(p *problems) {
	for path, colour := range map[string]string{
		"frontend.no_data_colour":       c.Frontend.NoDataColour,
		"frontend.marker_stroke_colour": c.Frontend.MarkerStrokeColour,
		"frontend.empty_basemap_colour": c.Frontend.EmptyBasemapColour,
		"frontend.chart_line_colour":    c.Frontend.ChartLineColour,
	} {
		if !colourPattern.MatchString(colour) {
			p.addf("%s = %q, must be a six-digit hex colour such as #9ca3af", path, colour)
		}
	}
	for path, zoom := range map[string]int{
		"frontend.zoom_city":   c.Frontend.ZoomCity,
		"frontend.zoom_sensor": c.Frontend.ZoomSensor,
	} {
		if zoom < 0 || zoom > 24 {
			p.addf("%s = %d, must be between 0 and 24", path, zoom)
		}
	}
	if c.Frontend.ZoomCity >= c.Frontend.ZoomSensor {
		p.addf("frontend.zoom_city (%d) must be below frontend.zoom_sensor (%d); the tiers are country, then city, then sensor", c.Frontend.ZoomCity, c.Frontend.ZoomSensor)
	}
	if c.Basemap.StyleURL == "" {
		p.addf("basemap.style_url is empty")
	} else if u, err := url.Parse(c.Basemap.StyleURL); err != nil {
		p.addf("basemap.style_url is not a URL: %v", err)
	} else {
		if u.Scheme != "http" && u.Scheme != "https" {
			p.addf("basemap.style_url must use http or https")
		}
		// Userinfo would put a credential in a URL the browser fetches, and the
		// CSP widens connect-src and img-src by this URL's host.
		if u.User != nil {
			p.addf("basemap.style_url must not contain userinfo")
		}
		if len(u.Host) > maxHostLength {
			p.addf("basemap.style_url host is %d bytes, must be at most %d", len(u.Host), maxHostLength)
		}
		if !hostPattern.MatchString(u.Host) {
			p.addf("basemap.style_url host = %q is not a valid hostname", u.Host)
		}
	}
}
