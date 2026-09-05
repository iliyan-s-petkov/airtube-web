package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func good(t *testing.T) Config {
	t.Helper()
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile(airbg.yaml) error = %v, want nil", err)
	}
	return cfg
}

// The committed file plus a database URL must be valid. If this fails the
// shipped configuration cannot start.
func TestCommittedConfigValidates(t *testing.T) {
	_ = good(t)
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"metrics addr equal to public addr", func(c *Config) { c.Listen.MetricsAddr = c.Listen.Addr }, "must be separate"},
		{"zero max_conns", func(c *Config) { c.Listen.MaxConns = 0 }, "listen.max_conns"},
		{"unsafe-inline in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-inline'" }, "unsafe-inline"},
		{"unsafe-eval in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-eval'" }, "unsafe-eval"},
		{"bad cidr", func(c *Config) { c.Listen.TrustedProxyCIDRs = []string{"10.0.0.1"} }, "not a CIDR"},
		{"missing database url", func(c *Config) { c.Database.URL = "" }, DatabaseURLEnv},
		{"series timeout above default", func(c *Config) { c.Database.StatementTimeouts.Series = time.Minute }, "tighter budget"},
		{"burst below rate", func(c *Config) { c.RateLimit.Series.Burst = 0.5 }, "below .per_second"},
		{"evict beyond ttl", func(c *Config) { c.RateLimit.API.EvictInterval = 2 * time.Hour }, "outlive their bucket"},
		{"zero enumerate areas", func(c *Config) { c.RateLimit.Enumerate.AreasPerWindow = 0 }, "areas_per_window"},
		{"poll below floor", func(c *Config) { c.Upstream.PollInterval = 10 * time.Second }, "min_poll_interval"},
		{"cache above half poll", func(c *Config) { c.Cache.DataMaxAge = 4 * time.Minute }, "half of upstream.poll_interval"},
		{"zero coverage threshold", func(c *Config) { c.Store.CoverageThreshold = 0 }, "single sensor"},
		{"unknown default metric", func(c *Config) { c.Series.DefaultMetric = "PM9" }, "not a canonical metric"},
		{"default window matches no period", func(c *Config) { c.Series.DefaultWindow = 3 * time.Hour }, "matches no entry"},
		{"missing metric range", func(c *Config) { delete(c.Quality.Ranges, "pressure") }, "no entry for \"pressure\""},
		{"inverted range", func(c *Config) { c.Quality.Ranges["pressure"] = Range{Min: 1100, Max: 650} }, "must exceed min"},
		{"rejection fraction above one", func(c *Config) { c.Backfill.HighRejectionFraction = 1.5 }, "high_rejection_fraction"},
		{"bad colour", func(c *Config) { c.Frontend.NoDataColour = "grey" }, "hex colour"},
		{"zoom tiers inverted", func(c *Config) { c.Frontend.ZoomCity = 12 }, "must be below"},
		// Ported from the deleted internal/config/config_test.go (Task 8, Step
		// 1): these assert rules Validate() still owns, even though the old
		// loader tested them at env-parse time instead of validation time.
		{"malformed base_url", func(c *Config) { c.Listen.BaseURL = "not-a-url" }, "must use http or https"},
		{"relative base_url", func(c *Config) { c.Listen.BaseURL = "/path/to/site" }, "must use http or https"},
		{"zero database api_conns", func(c *Config) { c.Database.APIConns = 0 }, "database.api_conns"},
		{"zero database collector_conns", func(c *Config) { c.Database.CollectorConns = 0 }, "database.collector_conns"},
		{"zero database max_inflight", func(c *Config) { c.Database.MaxInflight = 0 }, "database.max_inflight"},
		// The equivalent of the deleted basemap.style_url host checks, carried
		// forward onto tiles.public_url: httpx.CSP-style header concatenation
		// means a host containing any of these characters could inject a new
		// CSP directive (up to reintroducing 'unsafe-inline') the moment it
		// reaches a response. hostPattern's charset is the only thing standing
		// between an operator-supplied host and that outcome.
		{"tiles host semicolon", func(c *Config) {
			c.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example;object-src"}
		}, "not a valid hostname"},
		{"tiles host double quote", func(c *Config) {
			c.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example\"evil"}
		}, "not a valid hostname"},
		{"tiles host single quote", func(c *Config) {
			c.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example'evil"}
		}, "not a valid hostname"},
		{"tiles host comma", func(c *Config) {
			c.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example,evil"}
		}, "not a valid hostname"},
		// httpx.CSP concatenates the host into the policy twice (img-src and
		// connect-src), so an unbounded host doubles into an oversized header on
		// every response.
		{"tiles host longer than DNS limit", func(c *Config) {
			c.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://" + strings.Repeat("a", 254) + ".example"}
		}, "must be at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := good(t)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want a rejection mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// Every violation must be reported in one pass. One-per-restart is a validator
// bug when the file has forty keys.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := good(t)
	cfg.Listen.MaxConns = 0
	cfg.Store.CoverageThreshold = 0
	cfg.Frontend.NoDataColour = "grey"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want three problems")
	}
	for _, want := range []string{"listen.max_conns", "store.coverage_threshold", "hex colour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// Ported from the deleted config_test.go's TestAcceptsBasemapHostWithPort: a
// self-hosted tile server on a non-standard port is a legitimate deployment,
// and hostPattern must accept "host:port" rather than only a bare host.
func TestValidateAcceptsTilesHostWithPort(t *testing.T) {
	cfg := good(t)
	cfg.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: "https://tiles.example:8443"}
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.example:8443"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a host:port tiles.public_url accepted", err)
	}
}

// Ported from the deleted config_test.go's TestLoadAcceptsExactlyTheMinimum:
// the poll-interval floor is inclusive, so a deployment configured at exactly
// upstream.min_poll_interval must validate.
func TestValidateAcceptsPollIntervalAtTheFloor(t *testing.T) {
	cfg := good(t)
	cfg.Upstream.PollInterval = cfg.Upstream.MinPollInterval
	// cache.data_max_age must also stay within half of the (now much shorter)
	// poll_interval — that is a separate rule (validateUpstreamAndCache) this
	// test is not exercising, so satisfy it rather than let it mask the
	// assertion this test exists for.
	cfg.Cache.DataMaxAge = cfg.Upstream.PollInterval / 2
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want poll_interval == min_poll_interval accepted", err)
	}
}

// TestValidateRejectsEmptyUnscaledColour. unscaled_colour is the paint value
// for "this metric has no air-quality scale" (temperature, humidity,
// pressure, the two noise metrics) and must be present like every other
// frontend colour — an empty value here is a config bug, not a legal "use
// nothing" state.
func TestValidateRejectsEmptyUnscaledColour(t *testing.T) {
	c := validConfig(t)
	c.Frontend.UnscaledColour = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "frontend.unscaled_colour") {
		t.Fatalf("Validate() = %v, want an error naming frontend.unscaled_colour", err)
	}
}

// archiveName is a representative tiles.archive: a plain, dated PMTiles
// filename, the shape docs/tiles.md tells the operator to generate.
const archiveName = "bulgaria-20260815.pmtiles"

// validConfig loads the committed configuration, so these tests mutate the
// values the service actually ships with rather than a second copy that drifts.
func validConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}

// TestTilesAllOrNothing. Three of four keys set is the shape that produces a
// running server with a map that silently fetches from nowhere.
func TestTilesAllOrNothing(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"addr only":       func(c *Config) { c.Tiles = Tiles{Addr: "127.0.0.1:8082"} },
		"dir only":        func(c *Config) { c.Tiles = Tiles{Dir: "/var/lib/airbg/tiles"} },
		"public_url only": func(c *Config) { c.Tiles = Tiles{PublicURL: "https://tiles.airbg.org"} },
		"archive only":    func(c *Config) { c.Tiles = Tiles{Archive: archiveName} },
		"missing dir": func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", PublicURL: "https://tiles.airbg.org", Archive: archiveName}
		},
		// The key this fix added, in the shape that would otherwise slip through:
		// everything configured except the archive name, which the handler then
		// has no way to serve.
		"missing archive": func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: "https://tiles.airbg.org"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			mutate(&cfg)
			// Satisfy the connect-src coupling, so the only rule left that can
			// reject these is the all-or-nothing one. Without this the "missing
			// archive" case passes on the CSP error instead and proves nothing
			// about the key it is named for.
			cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate returned nil, want an error: tiles.* is all-or-nothing")
			}
		})
	}
}

// TestTilesEmptyIsLegal. No tiles configured means no basemap: the map island
// renders markers over frontend.empty_basemap_colour. Local development must
// not need a 300 MB file.
func TestTilesEmptyIsLegal(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = Tiles{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with no tiles configured = %v, want nil", err)
	}
	if cfg.Tiles.Enabled() {
		t.Error("Enabled() = true with every key empty")
	}
	if got := cfg.Tiles.StyleURL(); got != "" {
		t.Errorf("StyleURL() = %q, want empty", got)
	}
}

// TestTilesHostMustBeInConnectSrc. MapLibre fetches the style, the glyphs and
// the .pmtiles ranges over fetch/XHR. A CSP that omits the host fails closed and
// the map is blank, with nothing in any server log to say why.
func TestTilesHostMustBeInConnectSrc(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = Tiles{
		Addr:      "127.0.0.1:8082",
		Dir:       "/var/lib/airbg/tiles",
		PublicURL: "https://tiles.airbg.org",
		Archive:   archiveName,
	}
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self'"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want an error: connect-src omits tiles.airbg.org")
	}

	// A connect-src token that merely contains the host as a substring must not
	// satisfy the check: "not-tiles.airbg.org" contains "tiles.airbg.org", but
	// it names a different origin and the browser will still block the fetch.
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://not-tiles.airbg.org"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want an error: connect-src only allows a different origin that happens to contain the host as a substring")
	}

	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with the host in connect-src = %v, want nil", err)
	}
	if got, want := cfg.Tiles.StyleURL(), "https://tiles.airbg.org/style.json"; got != want {
		t.Errorf("StyleURL() = %q, want %q", got, want)
	}

	// The bare-host form (no scheme) is also a valid CSP source expression;
	// an operator may reasonably write either form.
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' tiles.airbg.org"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with the bare host in connect-src = %v, want nil", err)
	}
}

// TestTilesAddrIsSeparate. Sharing a listener address with the application or
// the metrics listener is the "three listeners simplified back to two" mistake
// in configuration form.
func TestTilesAddrIsSeparate(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:9090"} {
		cfg := validConfig(t)
		cfg.Listen.Addr = "127.0.0.1:8080"
		cfg.Listen.MetricsAddr = "127.0.0.1:9090"
		cfg.Tiles = Tiles{
			Addr:      addr,
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: "https://tiles.airbg.org",
			Archive:   archiveName,
		}
		cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate with tiles.addr = %q returned nil, want an error", addr)
		}
	}
}

// TestTilesArchiveShape. tiles.archive names a file inside tiles.dir and
// nothing else. internal/tiles reads through os.DirFS, so a traversing name
// could not escape the directory anyway — this gate exists so the operator gets
// a startup error naming the key, instead of a handler that finds the file and
// then 404s every request for it because the allowlist takes one path segment.
func TestTilesArchiveShape(t *testing.T) {
	for name, tc := range map[string]struct{ archive, want string }{
		"dated filename": {"bulgaria-20260815.pmtiles", ""},
		"plain filename": {"bulgaria.pmtiles", ""},
		"subdirectory":   {"archives/bulgaria.pmtiles", "plain filename"},
		"leading slash":  {"/var/lib/airbg/bulgaria.pmtiles", "plain filename"},
		"traversal":      {"../bulgaria.pmtiles", "plain filename"},
		"backslash":      {`archives\bulgaria.pmtiles`, "plain filename"},
		"dot":            {".", "plain filename"},
		"dotdot":         {"..", "plain filename"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Tiles = Tiles{
				Addr:      "127.0.0.1:8082",
				Dir:       "/var/lib/airbg/tiles",
				PublicURL: "https://tiles.airbg.org",
				Archive:   tc.archive,
			}
			cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate with tiles.archive = %q returned %v, want nil", tc.archive, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate with tiles.archive = %q returned nil, want an error mentioning %q", tc.archive, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate with tiles.archive = %q returned %v, want a message mentioning %q", tc.archive, err, tc.want)
			}
		})
	}
}

// TestTilesAllowedOriginsShape. Every entry here is compared to a browser's
// Origin header byte for byte, so any of these shapes produces an allowlist
// entry that matches nothing — and the symptom is identical to the problem the
// operator added it to fix: the other host still cannot read the tiles, and no
// message anywhere says why. Startup is the only place that is visible.
func TestTilesAllowedOriginsShape(t *testing.T) {
	for name, tc := range map[string]struct{ origin, want string }{
		"plain https": {"https://kit.example", ""},
		"http":        {"http://localhost:5173", ""},
		"with a port": {"https://kit.example:8443", ""},
		"no scheme":   {"kit.example", "is not one of http, https"},
		"ftp":         {"ftp://kit.example", "is not one of http, https"},
		// Refused for the same reason as ftp: a scheme is only allowed once an
		// operator has declared it. TestOriginSchemesAdmitADeclaredScheme
		// covers the other half.
		"undeclared custom scheme": {"od://app", "is not one of http, https"},
		"userinfo":                 {"https://user:pass@kit.example", "must not contain userinfo"},
		"empty host":               {"https:///", "names no host"},
		"empty entry":              {"", "empty entry"},
		"wildcard":                 {"*", "wildcards are not matched"},
		"scheme wildcard":          {"https://*.example", "wildcards are not matched"},
		// The two that look right and are not. url.Parse gives a trailing
		// slash a Path of "/", and an Origin header carries neither.
		"trailing slash": {"https://kit.example/", "no path or trailing slash"},
		"path":           {"https://kit.example/preview", "no path or trailing slash"},
		"query":          {"https://kit.example?a=b", "query or fragment"},
		"fragment":       {"https://kit.example#f", "query or fragment"},
	} {
		t.Run(name, func(t *testing.T) {
			// No tiles.* scalars: the origins are optional and validated
			// independently, so this also proves a list on its own does not
			// trip the all-or-nothing rule.
			cfg := validConfig(t)
			cfg.Tiles = Tiles{AllowedOrigins: []string{tc.origin}}
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate with tiles.allowed_origins = [%q] returned %v, want nil", tc.origin, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate with tiles.allowed_origins = [%q] returned nil, want an error mentioning %q", tc.origin, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate with tiles.allowed_origins = [%q] returned %v, want a message mentioning %q", tc.origin, err, tc.want)
			}
			// The bad value must be named. An operator with a five-entry list
			// and "one of these is wrong" is no better off than before.
			if tc.origin != "" && !strings.Contains(err.Error(), tc.origin) {
				t.Errorf("Validate error %v does not name the offending value %q", err, tc.origin)
			}
		})
	}
}

// TestOriginSchemesAdmitADeclaredScheme. Declaring a scheme is what lets an
// origin using it be NAMED; it must not admit the scheme wholesale. A desktop
// design tool previews from od://app, a real single origin that is neither
// loopback nor https, and the point of the key is that od://anything-else stays
// refused unless it too is listed.
func TestOriginSchemesAdmitADeclaredScheme(t *testing.T) {
	for name, tc := range map[string]struct {
		origins, schemes []string
		wantErr          bool
	}{
		"declared scheme, named origin":     {[]string{"od://app"}, []string{"od"}, false},
		"declared scheme, no origin named":  {nil, []string{"od"}, false},
		"undeclared scheme":                 {[]string{"od://app"}, nil, true},
		"a different scheme declared":       {[]string{"od://app"}, []string{"figma"}, true},
		"http still works when od declared": {[]string{"https://kit.example"}, []string{"od"}, false},
		// Declaring the scheme does not relax the rest of the shape rules: the
		// entry is still matched byte for byte against an Origin header.
		"declared scheme, trailing slash": {[]string{"od://app/"}, []string{"od"}, true},
		"declared scheme, with a path":    {[]string{"od://app/preview"}, []string{"od"}, true},
		"declared scheme, wildcard host":  {[]string{"od://*"}, []string{"od"}, true},
		// The schemes list itself. "od://" is the shape an operator reaches for
		// first, and it must not silently fail to match the origin's scheme.
		"scheme written as a prefix": {[]string{"od://app"}, []string{"od://"}, true},
		"scheme with a colon":        {[]string{"od://app"}, []string{"od:"}, true},
		"upper-case scheme":          {[]string{"od://app"}, []string{"OD"}, true},
		"empty scheme entry":         {nil, []string{""}, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Asserted on both surfaces in one loop: the two lists are separate
			// config keys sharing one validator, and a copy that drifted would
			// be a rule enforced on the basemap and not on the data API.
			for _, surface := range []string{"tiles", "listen"} {
				cfg := validConfig(t)
				switch surface {
				case "tiles":
					cfg.Tiles = Tiles{AllowedOrigins: tc.origins, AllowedOriginSchemes: tc.schemes}
				case "listen":
					cfg.Listen.AllowedOrigins = tc.origins
					cfg.Listen.AllowedOriginSchemes = tc.schemes
				}
				err := cfg.Validate()
				if tc.wantErr && err == nil {
					t.Errorf("%s.allowed_origins = %q with schemes %q returned nil, want an error",
						surface, tc.origins, tc.schemes)
				}
				if !tc.wantErr && err != nil {
					t.Errorf("%s.allowed_origins = %q with schemes %q returned %v, want nil",
						surface, tc.origins, tc.schemes, err)
				}
			}
		})
	}
}

// TestOriginSchemeSyntaxIsNamedAsSuch. A malformed scheme is refused twice
// over — once here and once by validateOrigins, which will not match "od://" to
// the scheme "od" either — so the entry is rejected regardless. What is only
// true if this check exists is that the operator is told WHICH key is wrong.
// Without it the message is about the origin, and an operator reading "od://app
// has an unknown scheme" after declaring od:// has been sent to correct the
// line that was already right.
func TestOriginSchemeSyntaxIsNamedAsSuch(t *testing.T) {
	for _, scheme := range []string{"od://", "od:", "OD", "1od", "od app"} {
		t.Run(scheme, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Listen.AllowedOriginSchemes = []string{scheme}
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("listen.allowed_origin_schemes = [%q] returned nil, want an error", scheme)
			}
			if !strings.Contains(err.Error(), "listen.allowed_origin_schemes") {
				t.Errorf("error %v does not name listen.allowed_origin_schemes as the wrong key", err)
			}
			if !strings.Contains(err.Error(), "name a scheme alone") {
				t.Errorf("error %v does not say how to write a scheme", err)
			}
		})
	}
}

// TestListenAllowedOriginsAreSeparateFromTiles. The two keys exist so that
// trusting an origin with the public basemap does not silently also trust it
// with the data API. If either list were ever wired to both surfaces, one of
// these halves fails.
func TestListenAllowedOriginsAreSeparateFromTiles(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = Tiles{AllowedOrigins: []string{"od://app"}, AllowedOriginSchemes: []string{"od"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("declaring od for tiles alone returned %v, want nil", err)
	}
	if got := cfg.Listen.AllowedOrigins; len(got) != 0 {
		t.Errorf("listen.allowed_origins = %q after configuring tiles only, want empty", got)
	}

	// And the reverse: declaring od on listen must not make it legal in the
	// tiles list, which is the same key name one level up.
	cfg = validConfig(t)
	cfg.Listen.AllowedOriginSchemes = []string{"od"}
	cfg.Tiles = Tiles{AllowedOrigins: []string{"od://app"}}
	if err := cfg.Validate(); err == nil {
		t.Error("listen.allowed_origin_schemes made od://app legal in tiles.allowed_origins, want an error")
	}
}

// TestTilesPublicURLShape. The host reaches a Content-Security-Policy header
// assembled by concatenation, so anything but a plain absolute http(s) URL is
// rejected — the same rule the deleted basemap.style_url carried.
func TestTilesPublicURLShape(t *testing.T) {
	// want is a distinctive fragment of the message each case must produce.
	// Asserting the message, not merely "some error": several of these URLs also
	// fail the connect-src coupling below, so a bare non-nil check would pass
	// even with the shape gate deleted.
	for name, tc := range map[string]struct{ url, want string }{
		"no scheme":  {"tiles.airbg.org", "must use http or https"},
		"ftp":        {"ftp://tiles.airbg.org", "must use http or https"},
		"userinfo":   {"https://user:pass@tiles.airbg.org", "must not contain userinfo"},
		"space":      {"https://tiles airbg.org", "tiles.public_url"},
		"empty host": {"https:///style.json", "is not a valid hostname"},
		// A path is the case the gate exists for and the one nothing caught:
		// StyleURL yields ".../basemap/style.json", the handler 404s two
		// segments, and the connect-src check still passes because it matches
		// on the host alone. Blank map, no server-side error.
		"path":            {"https://tiles.airbg.org/basemap", "no path"},
		"trailing path":   {"https://tiles.airbg.org/basemap/", "no path"},
		"query":           {"https://tiles.airbg.org?key=secret", "query string"},
		"fragment":        {"https://tiles.airbg.org#frag", "fragment"},
		"path and query":  {"https://tiles.airbg.org/a?b=c", "no path"},
		"root path is ok": {"https://tiles.airbg.org/", ""},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Tiles = Tiles{Archive: archiveName, Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: tc.url}
			// The origin, not the raw value: the host coupling must be satisfied
			// so that only the shape gate can reject these.
			cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate with tiles.public_url = %q returned %v, want nil", tc.url, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate with tiles.public_url = %q returned nil, want an error mentioning %q", tc.url, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate with tiles.public_url = %q returned %v, want a message mentioning %q", tc.url, err, tc.want)
			}
		})
	}
}
