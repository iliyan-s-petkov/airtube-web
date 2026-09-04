package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearAmbientEnv unsets every AIRBG_* variable for the duration of the test.
//
// Without it this test reads the developer's or CI runner's shell: an ambient
// AIRBG_STORE_COVERAGE_THRESHOLD=99 makes it report
// "store.coverage_threshold = 99, want 3" while airbg.yaml is untouched — a
// failure that blames the wrong file. The proof this test exists to give is
// about the committed file, so the environment layer must be out of the way.
//
// os.Setenv/os.Unsetenv with an explicit restore rather than t.Setenv: t.Setenv
// cannot unset a variable, only assign one.
func clearAmbientEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, "AIRBG_") {
			continue
		}
		saved, had := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%s): %v", name, err)
		}
		if had {
			t.Cleanup(func() { _ = os.Setenv(name, saved) })
		}
	}
}

// TestShippedValuesMatchPhase2Behaviour pins every value that Phase 3b moved out
// of code. The want column is the constant as it existed before the sweep,
// named in the comment. A failure here means the configuration sweep changed
// behaviour — which it is not allowed to do.
//
// Retuning any of these later is legitimate. Changing this test without saying
// why in the commit message is not.
func TestShippedValuesMatchPhase2Behaviour(t *testing.T) {
	// Order matters: clear the ambient environment first, then set the one
	// variable this test does need.
	clearAmbientEnv(t)
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}

	t.Run("durations", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			got  time.Duration
			want time.Duration
		}{
			{"timeouts.read_header", cfg.Timeouts.ReadHeader, 5 * time.Second},
			{"timeouts.read", cfg.Timeouts.Read, 10 * time.Second},
			{"timeouts.write", cfg.Timeouts.Write, 30 * time.Second},
			{"timeouts.idle", cfg.Timeouts.Idle, 60 * time.Second},
			{"timeouts.shutdown_grace", cfg.Timeouts.ShutdownGrace, 15 * time.Second},
			{"database.statement_timeouts.default", cfg.Database.StatementTimeouts.Default, 15 * time.Second},
			{"database.statement_timeouts.assign", cfg.Database.StatementTimeouts.Assign, 60 * time.Second},
			{"database.statement_timeouts.operator", cfg.Database.StatementTimeouts.Operator, 10 * time.Minute},
			{"database.statement_timeouts.series", cfg.Database.StatementTimeouts.Series, 5 * time.Second},
			{"ratelimit.api.ttl", cfg.RateLimit.API.TTL, 30 * time.Minute},
			{"ratelimit.api.evict_interval", cfg.RateLimit.API.EvictInterval, 5 * time.Minute},
			{"ratelimit.series.ttl", cfg.RateLimit.Series.TTL, 30 * time.Minute},
			{"ratelimit.series.evict_interval", cfg.RateLimit.Series.EvictInterval, 5 * time.Minute},
			{"ratelimit.series.retry_after", cfg.RateLimit.Series.RetryAfter, 2 * time.Second},
			{"ratelimit.enumerate.window", cfg.RateLimit.Enumerate.Window, time.Hour},
			{"ratelimit.enumerate.retry_after", cfg.RateLimit.Enumerate.RetryAfter, 900 * time.Second},
			{"cache.data_max_age", cfg.Cache.DataMaxAge, 150 * time.Second},
			{"cache.scales_max_age", cfg.Cache.ScalesMaxAge, 86400 * time.Second},
			{"upstream.request_timeout", cfg.Upstream.RequestTimeout, 30 * time.Second},
			{"upstream.poll_interval", cfg.Upstream.PollInterval, 5 * time.Minute},
			{"upstream.min_poll_interval", cfg.Upstream.MinPollInterval, 30 * time.Second},
			{"store.freshness_window", cfg.Store.FreshnessWindow, 2 * time.Hour},
			{"series.default_window", cfg.Series.DefaultWindow, 24 * time.Hour},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		}
	})

	t.Run("numbers", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			got  float64
			want float64
		}{
			{"listen.max_conns", float64(cfg.Listen.MaxConns), 4096},
			{"database.api_conns", float64(cfg.Database.APIConns), 8},
			{"database.collector_conns", float64(cfg.Database.CollectorConns), 4},
			{"database.max_inflight", float64(cfg.Database.MaxInflight), 16},
			{"ratelimit.api.per_second", cfg.RateLimit.API.PerSecond, 10},
			{"ratelimit.api.burst", cfg.RateLimit.API.Burst, 60},
			{"ratelimit.series.per_second", cfg.RateLimit.Series.PerSecond, 1},
			{"ratelimit.series.burst", cfg.RateLimit.Series.Burst, 10},
			// The one deliberate divergence from Phase 2 behaviour: raised from
			// 12 because Bulgaria has 28 oblasti and comparing them is the
			// site's obvious use. Set to 30 during the deployment branch, then
			// settled at 20 by the owner once the review put a number on the
			// trade — 30 let one address sweep the whole ~80-page corpus in
			// under three hours. See
			// docs/superpowers/specs/2026-08-17-airbg-deployment-design.md.
			// Every other row in this table still means "unchanged since Phase 2".
			{"ratelimit.enumerate.areas_per_window", float64(cfg.RateLimit.Enumerate.AreasPerWindow), 20},
			{"ratelimit.enumerate.sensors_per_window", float64(cfg.RateLimit.Enumerate.SensorsPerWindow), 40},
			{"ratelimit.shard_count", float64(cfg.RateLimit.ShardCount), 32},
			{"upstream.max_payload_bytes", float64(cfg.Upstream.MaxPayloadBytes), 64 << 20},
			{"store.coverage_threshold", float64(cfg.Store.CoverageThreshold), 3},
			{"quality.min_neighbours", float64(cfg.Quality.MinNeighbours), 3},
			{"quality.mad_scale", cfg.Quality.MADScale, 1.4826},
			{"quality.mad_threshold", cfg.Quality.MADThreshold, 3.5},
			{"quality.neighbour_radius_metres", cfg.Quality.NeighbourRadiusMetres, 15000},
			{"quality.earth_radius_metres", cfg.Quality.EarthRadiusMetres, 6371000},
			{"quality.history_depth", float64(cfg.Quality.HistoryDepth), 12},
			// quality/spatial.go's pmRatioThreshold and pmAbsoluteThreshold.
			{"quality.pm_ratio_threshold", cfg.Quality.PMRatioThreshold, 5.0},
			{"quality.pm_absolute_threshold", cfg.Quality.PMAbsoluteThreshold, 150.0},
			// quality/spatial.go's smoothFieldFloors, entry by entry.
			{"quality.smooth_field_floors.temperature", cfg.Quality.SmoothFieldFloors["temperature"], 1.5},
			{"quality.smooth_field_floors.humidity", cfg.Quality.SmoothFieldFloors["humidity"], 8},
			{"quality.smooth_field_floors.pressure", cfg.Quality.SmoothFieldFloors["pressure"], 3},
			// Membership is behaviour, not just the values: PM and noise were
			// absent from the map this replaced, and a fourth entry would start
			// spatially checking a metric that was never checked before.
			{"len(quality.smooth_field_floors)", float64(len(cfg.Quality.SmoothFieldFloors)), 3},
			{"backfill.high_rejection_fraction", cfg.Backfill.HighRejectionFraction, 0.5},
			{"frontend.zoom_city", float64(cfg.Frontend.ZoomCity), 9},
			{"frontend.zoom_sensor", float64(cfg.Frontend.ZoomSensor), 11},
			// api/locate.go's defaultZoom/defaultLon/defaultLat, which
			// index.gohtml also carried as attribute literals.
			{"frontend.default_zoom", float64(cfg.Frontend.DefaultZoom), 7},
			{"frontend.default_lon", cfg.Frontend.DefaultLon, 25.4858},
			{"frontend.default_lat", cfg.Frontend.DefaultLat, 42.7339},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		}
	})

	t.Run("ranges", func(t *testing.T) {
		want := map[string]Range{
			"P1":           {0, 1000},
			"P2":           {0, 1000},
			"temperature":  {-40, 60},
			"humidity":     {0, 100},
			"pressure":     {650, 1100},
			"noise_LAeq":   {25, 120},
			"noise_LA_max": {25, 120},
		}
		for metric, w := range want {
			if got := cfg.Quality.Ranges[metric]; got != w {
				t.Errorf("quality.ranges.%s = %+v, want %+v", metric, got, w)
			}
		}
	})

	t.Run("strings", func(t *testing.T) {
		for _, tt := range []struct{ name, got, want string }{
			{"listen.addr", cfg.Listen.Addr, "127.0.0.1:8080"},
			{"listen.metrics_addr", cfg.Listen.MetricsAddr, "127.0.0.1:9090"},
			{"listen.base_url", cfg.Listen.BaseURL, "http://localhost:8080"},
			// The country filter moved out of the url and into
			// upstream.countries, which builds it — see fetchURL and
			// TestCommittedConfigEnablesBulgariaAndItsNeighbours.
			{"upstream.url", cfg.Upstream.URL, "https://data.sensor.community/airrohr/v1/filter/"},
			{"series.default_metric", cfg.Series.DefaultMetric, "P2"},
			{"frontend.no_data_colour", cfg.Frontend.NoDataColour, "#9ca3af"},
			{"frontend.marker_stroke_colour", cfg.Frontend.MarkerStrokeColour, "#ffffff"},
			{"frontend.marker_label_colour", cfg.Frontend.MarkerLabelColour, "#161616"},
			{"frontend.empty_basemap_colour", cfg.Frontend.EmptyBasemapColour, "#eef2f5"},
			{"frontend.chart_line_colour", cfg.Frontend.ChartLineColour, "#2563eb"},
			// The tiles keys are NEW, not moved: this pin records the decision that
			// the shipped configuration has no basemap, rather than proving a
			// non-change. Configuring one is a deployment step (docs/tiles.md).
			{"tiles.addr", cfg.Tiles.Addr, ""},
			{"tiles.dir", cfg.Tiles.Dir, ""},
			{"tiles.public_url", cfg.Tiles.PublicURL, ""},
			{"tiles.archive", cfg.Tiles.Archive, ""},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		}
		// Not in the table above because it is a list, and because what it pins
		// is different: listen.base_url may read the basemap without being
		// listed, so a non-empty shipped list would be a second origin nobody
		// asked for.
		if got := cfg.Tiles.AllowedOrigins; len(got) != 0 {
			t.Errorf("tiles.allowed_origins = %q, want empty; the site's own origin is allowed without being listed", got)
		}
		// A bool, so it is not in the table either. Shipped off: it widens the
		// allowlist by rule rather than by name, and an operator who has not
		// asked for a preview host must not get one.
		if cfg.Tiles.AllowLoopbackOrigins {
			t.Error("tiles.allow_loopback_origins = true, want false; it is an opt-in for design-preview hosts")
		}
	})

	t.Run("csp", func(t *testing.T) {
		// The exact Phase 1 §9.7 policy, reassembled. The YAML folded scalar must
		// produce this byte for byte, or the shipped policy is not the reviewed one.
		want := "default-src 'self'; script-src 'self'; style-src 'self'; " +
			"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; " +
			"worker-src 'self' blob:; object-src 'none'; base-uri 'none'; " +
			"form-action 'none'; frame-ancestors 'none'"
		if cfg.Listen.CSP != want {
			t.Errorf("listen.csp =\n  %q\nwant\n  %q", cfg.Listen.CSP, want)
		}
	})
}
