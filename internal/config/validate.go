package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
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

// parseErrorReason extracts the underlying reason from a url.Parse failure
// without the input string url.Error.Error() would otherwise quote. Used
// wherever the parsed URL comes from operator input, such as tiles.public_url.
func parseErrorReason(err error) string {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err.Error()
	}
	return err.Error()
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
	c.validateTiles(&p)

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
		p.addf("%s is not set in the environment (directly, or via %s naming a file); it is required and must never be written to the config file", DatabaseURLEnv, DatabaseURLFileEnv)
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
		"ratelimit.series": c.RateLimit.Series.Bucket,
	} {
		p.positiveFloat(path+".per_second", b.PerSecond)
		p.positiveFloat(path+".burst", b.Burst)
		p.positive(path+".ttl", b.TTL)
		p.positive(path+".evict_interval", b.EvictInterval)
		if b.Burst < b.PerSecond {
			p.addf("%s.burst (%v) is below .per_second (%v); the bucket could never fill for one second of traffic", path, b.Burst, b.PerSecond)
		}
		if b.EvictInterval > b.TTL {
			p.addf("%s.evict_interval (%v) exceeds .ttl (%v); entries would outlive their bucket", path, b.EvictInterval, b.TTL)
		}
	}
	// Only the series bucket carries a retry_after: it is the 503 admission hint,
	// not a rate-limit value. The API bucket's 429 Retry-After is computed.
	p.positive("ratelimit.series.retry_after", c.RateLimit.Series.RetryAfter)
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
		p.positive(fmt.Sprintf("series.periods[%s].bucket", name), pd.Bucket)
		p.positive(fmt.Sprintf("series.periods[%s].max_age", name), pd.MaxAge)
		// A bucket at least as wide as the window collapses the chart to a
		// single point, which renders as an empty plot rather than an error.
		if pd.Bucket > 0 && pd.Window > 0 && pd.Bucket >= pd.Window {
			p.addf("series.periods[%s].bucket (%v) is not smaller than its window (%v)", name, pd.Bucket, pd.Window)
		}
		// An hourly period reads the hourly rollup, so a sub-hour bucket cannot
		// add resolution — it only splits one row per hour across empty buckets.
		if pd.Hourly && pd.Bucket > 0 && pd.Bucket < time.Hour {
			p.addf("series.periods[%s].bucket (%v) is under an hour but hourly is true", name, pd.Bucket)
		}
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
	// Both PM guards must be positive: a zero ratio or a zero absolute floor
	// turns "flag only what is both relatively and absolutely extreme" into
	// "flag every reading above the median", which discards the point-source
	// spikes that are the signal PM monitoring exists for.
	p.positiveFloat("quality.pm_ratio_threshold", q.PMRatioThreshold)
	p.positiveFloat("quality.pm_absolute_threshold", q.PMAbsoluteThreshold)
	// A zero or negative floor lets an unusually tight neighbourhood (MAD near
	// zero) flag ordinary variation as an outlier.
	for _, metric := range []string{"temperature", "humidity", "pressure"} {
		p.positiveFloat("quality.smooth_field_floors."+metric, q.SmoothFieldFloors[metric])
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
		"frontend.unscaled_colour":      c.Frontend.UnscaledColour,
		"frontend.marker_stroke_colour": c.Frontend.MarkerStrokeColour,
		"frontend.marker_label_colour":  c.Frontend.MarkerLabelColour,
		"frontend.empty_basemap_colour": c.Frontend.EmptyBasemapColour,
		"frontend.chart_line_colour":    c.Frontend.ChartLineColour,
	} {
		if !colourPattern.MatchString(colour) {
			p.addf("%s = %q, must be a six-digit hex colour such as #9ca3af", path, colour)
		}
	}
	for path, zoom := range map[string]int{
		"frontend.zoom_city":    c.Frontend.ZoomCity,
		"frontend.zoom_sensor":  c.Frontend.ZoomSensor,
		"frontend.default_zoom": c.Frontend.DefaultZoom,
	} {
		if zoom < 0 || zoom > 24 {
			p.addf("%s = %d, must be between 0 and 24", path, zoom)
		}
	}
	// The fallback view is shown to a visitor the service knows nothing about,
	// so an off-globe coordinate would be a blank map with no error anywhere.
	if lon := c.Frontend.DefaultLon; lon < -180 || lon > 180 {
		p.addf("frontend.default_lon = %v, must be between -180 and 180", lon)
	}
	if lat := c.Frontend.DefaultLat; lat < -90 || lat > 90 {
		p.addf("frontend.default_lat = %v, must be between -90 and 90", lat)
	}
	if c.Frontend.ZoomCity >= c.Frontend.ZoomSensor {
		p.addf("frontend.zoom_city (%d) must be below frontend.zoom_sensor (%d); the tiers are country, then city, then sensor", c.Frontend.ZoomCity, c.Frontend.ZoomSensor)
	}
}

// validateTiles checks the two couplings the self-hosted basemap depends on.
// Both fail silently at runtime — a blank map and no server-side error — so
// both fail loudly at startup instead.
func (c Config) validateTiles(p *problems) {
	set := map[string]string{
		"tiles.addr":       c.Tiles.Addr,
		"tiles.dir":        c.Tiles.Dir,
		"tiles.public_url": c.Tiles.PublicURL,
		"tiles.archive":    c.Tiles.Archive,
	}
	var empty, filled []string
	for path, v := range set {
		if v == "" {
			empty = append(empty, path)
		} else {
			filled = append(filled, path)
		}
	}
	// Checked before the all-or-nothing gate below, and outside it: the origins
	// are optional, so a list on its own is not a half-configured basemap — but
	// a malformed entry in one is still worth refusing at startup rather than
	// letting it sit in the allowlist matching nothing.
	c.validateTileOrigins(p)

	if len(filled) == 0 {
		// No basemap configured. Legal: the map renders markers over
		// frontend.empty_basemap_colour, and local development needs neither a
		// vendor account nor a 300 MB file.
		return
	}
	if len(empty) > 0 {
		sort.Strings(empty)
		p.addf("tiles.* is all-or-nothing; %s is set but %s is empty",
			strings.Join(sorted(filled), ", "), strings.Join(empty, ", "))
		return
	}

	if len(c.Tiles.Addr) > maxHostLength || !hostPattern.MatchString(c.Tiles.Addr) {
		p.addf("tiles.addr = %q, must be host:port", c.Tiles.Addr)
	}
	// A third listener that shares an address with either of the other two is
	// the "three listeners simplified back to two" mistake, in configuration.
	if c.Tiles.Addr == c.Listen.Addr {
		p.addf("tiles.addr and listen.addr are both %q; the tiles listener must be separate", c.Tiles.Addr)
	}
	if c.Tiles.Addr == c.Listen.MetricsAddr {
		p.addf("tiles.addr and listen.metrics_addr are both %q; the tiles listener must be separate", c.Tiles.Addr)
	}

	// A plain filename inside tiles.dir, nothing else. This is defence in depth
	// and a clearer error, not the primary control: internal/tiles reads through
	// os.DirFS, which already makes an escape from tiles.dir structurally
	// impossible. What this buys is that a name with a path in it fails here,
	// at startup, instead of passing the handler's existence check and then
	// being refused by its allowlist at request time — which is a blank map.
	if strings.ContainsAny(c.Tiles.Archive, `/\`) || c.Tiles.Archive == "." || c.Tiles.Archive == ".." {
		p.addf("tiles.archive = %q must be a plain filename inside tiles.dir, with no path separator", c.Tiles.Archive)
	}

	u, err := url.Parse(c.Tiles.PublicURL)
	switch {
	case err != nil:
		p.addf("tiles.public_url is not a URL: %s", parseErrorReason(err))
	case u.Scheme != "http" && u.Scheme != "https":
		p.addf("tiles.public_url must use http or https")
	case u.User != nil:
		// Userinfo would put a credential in a URL the browser fetches, and the
		// host below is concatenated into a CSP header.
		p.addf("tiles.public_url must not contain userinfo")
	case len(u.Host) > maxHostLength:
		p.addf("tiles.public_url host is %d bytes, must be at most %d", len(u.Host), maxHostLength)
	case !hostPattern.MatchString(u.Host):
		p.addf("tiles.public_url host = %q is not a valid hostname", u.Host)
	case u.Path != "" && u.Path != "/":
		// StyleURL only trims a trailing slash, so a path here produces
		// ".../basemap/style.json" — two segments, which the handler's allowlist
		// 404s. The CSP coupling below would still pass, because it matches on
		// the host: the map goes blank and every check that exists to catch that
		// says nothing. This is an origin, not a URL prefix.
		p.addf("tiles.public_url = %q must be an origin with no path; the tiles listener serves style.json, glyphs/ and the archive at its root", c.Tiles.PublicURL)
	case u.RawQuery != "":
		// The deleted basemap.style_url carried "?key=..."; a query here is the
		// vendor shape returning, and it would be concatenated into every
		// derived URL where nothing consumes it.
		p.addf("tiles.public_url = %q must not contain a query string", c.Tiles.PublicURL)
	case u.Fragment != "":
		p.addf("tiles.public_url = %q must not contain a fragment", c.Tiles.PublicURL)
	default:
		// MapLibre fetches the style, the glyphs and the .pmtiles ranges over
		// fetch/XHR. A connect-src that omits this host fails closed: a blank
		// map, and nothing anywhere on the server to say why.
		//
		// This must be an exact match against whitespace-separated connect-src
		// tokens, not a substring test: strings.Contains("not-tiles.airbg.org",
		// "tiles.airbg.org") is true, which would let a CSP that allows a
		// *different* origin satisfy the check for this one.
		//
		// The cost of exactness is that a wildcard source such as
		// "https://*.airbg.org" is not recognised, even though a browser would
		// honour it and the map would work. That is accepted rather than fixed:
		// matching wildcards means reimplementing CSP source-expression matching
		// here, and getting that subtly wrong turns a check that catches a real
		// misconfiguration into one that waves it through. So the message below
		// says what this check wants — the host, written literally — instead of
		// telling the operator their CSP is broken, which for a wildcard it is
		// not.
		origin := u.Scheme + "://" + u.Host
		found := false
		for _, tok := range strings.Fields(connectSrc(c.Listen.CSP)) {
			if tok == origin || tok == u.Host {
				found = true
				break
			}
		}
		if !found {
			p.addf("listen.csp's connect-src must list %q literally (as %q or %q); wildcard sources are not recognised here even though browsers honour them, so widen the CSP or add the exact host", u.Host, origin, u.Host)
		}
	}
}

// validateTileOrigins checks each extra origin allowed to read the basemap.
//
// The handler compares these to the browser's Origin header byte for byte, so
// every one of the shapes refused below — a trailing slash, a path, a wildcard
// — produces an entry that can never match anything. That failure is silent and
// looks exactly like the bug it was meant to fix: the other host still cannot
// read the tiles, and nothing on either side says why. Refusing at startup, by
// value, is the only place it is visible.
func (c Config) validateTileOrigins(p *problems) {
	for _, o := range c.Tiles.AllowedOrigins {
		const key = "tiles.allowed_origins"
		if o == "" {
			p.addf("%s contains an empty entry; remove it or name an origin", key)
			continue
		}
		// Named ahead of the parse because "*" parses cleanly as a path and
		// would otherwise be reported as the wrong problem entirely.
		if strings.Contains(o, "*") {
			p.addf("%s contains %q; wildcards are not matched, name each origin in full", key, o)
			continue
		}
		u, err := url.Parse(o)
		switch {
		case err != nil:
			p.addf("%s contains %q, which is not a URL: %s", key, o, parseErrorReason(err))
		case u.Scheme != "http" && u.Scheme != "https":
			p.addf("%s contains %q; an origin must use http or https", key, o)
		case u.User != nil:
			p.addf("%s contains %q; an origin must not contain userinfo", key, o)
		case u.Host == "":
			p.addf("%s contains %q, which names no host", key, o)
		case len(u.Host) > maxHostLength:
			p.addf("%s contains %q, whose host is %d bytes, must be at most %d", key, o, len(u.Host), maxHostLength)
		case !hostPattern.MatchString(u.Host):
			p.addf("%s contains %q, whose host is not a valid hostname", key, o)
		case u.Path != "":
			// Covers the trailing slash too: url.Parse gives "https://x/" a
			// Path of "/". A browser's Origin header never carries either, so
			// both are entries that match nothing.
			p.addf("%s contains %q; an origin is scheme and host only, with no path or trailing slash", key, o)
		case u.RawQuery != "" || u.Fragment != "":
			p.addf("%s contains %q; an origin must not carry a query or fragment", key, o)
		}
	}
}

// sorted returns a sorted copy, so a problem message reads the same on every
// run. Ranging a map is deliberately unordered in Go.
func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// connectSrc extracts the connect-src directive from a CSP, or default-src when
// connect-src is absent — the fallback the browser itself applies.
func connectSrc(csp string) string {
	var fallback string
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if name, rest, ok := strings.Cut(directive, " "); ok {
			switch name {
			case "connect-src":
				return rest
			case "default-src":
				fallback = rest
			}
		}
	}
	return fallback
}
