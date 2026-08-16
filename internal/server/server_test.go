package server_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/httpx"
	"airbg.org/internal/i18n"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// testConfig is the committed configuration, loaded once, so these tests
// exercise the values the service actually ships with (timeouts, rate limits,
// CSP, ...) rather than a second copy that can drift. Same shape as
// internal/api/router_test.go's testConfig — each package that needs one keeps
// its own copy.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}

func free(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// running starts a server with two listeners. tilesDir empty means no basemap,
// which is the shipped configuration; runningWithTiles covers the other state.
func running(t *testing.T) (public, private string) {
	pub, priv, _ := start(t, "")
	return pub, priv
}

func runningWithTiles(t *testing.T, tilesDir string, tweak ...func(*config.Config)) (public, private, tiles string) {
	return start(t, tilesDir, tweak...)
}

// start builds the server from the committed configuration. tweak runs after
// the addresses are assigned and before server.New, so a test can move a knob
// (the connection cap, say) without a second copy of this setup.
func start(t *testing.T, tilesDir string, tweak ...func(*config.Config)) (public, private, tilesAddr string) {
	t.Helper()

	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	holder := snapshot.NewHolder(cfg.Series)
	holder.Store(&snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs:  map[string]snapshot.AreaMeta{},
		Overview:    snapshot.Body{JSON: []byte(`{"areas":[]}`), ETag: `"t"`},
	})

	public, private = free(t), free(t)
	cfg.Listen.Addr = public
	cfg.Listen.MetricsAddr = private
	cfg.Listen.BaseURL = "http://" + public
	if tilesDir != "" {
		tilesAddr = free(t)
		cfg.Tiles = config.Tiles{
			Addr:      tilesAddr,
			Dir:       tilesDir,
			PublicURL: "http://" + tilesAddr,
		}
	}
	for _, fn := range tweak {
		fn(&cfg)
	}

	srv, err := server.New(server.Options{
		Config:    cfg,
		Catalogue: cat,
		Snapshots: holder,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return within 10s of cancellation; shutdown is not graceful, it is stuck")
		}
	})

	waitReady(t, private)
	return public, private, tilesAddr
}

// tilesDir writes a miniature tile directory, so these tests need no
// 300 MB artefact. The handler serves bytes and never parses them.
func tilesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "glyphs", "NotoSans-Regular"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"style.json":                        `{"version":8,"sources":{},"layers":[]}`,
		"bulgaria.pmtiles":                  "PMTilesFAKEBODY0123456789",
		"glyphs/NotoSans-Regular/0-255.pbf": "fakeglyphs",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the private listener never came up")
}

func get(t *testing.T, addr, path string) *http.Response {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPublicListenerServesPagesAndAPI(t *testing.T) {
	public, _ := running(t)

	if got := get(t, public, "/").StatusCode; got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	if got := get(t, public, "/api/v1/overview").StatusCode; got != http.StatusOK {
		t.Errorf("GET /api/v1/overview = %d, want 200", got)
	}
}

// TestMetricsAreNotOnThePublicListener. /metrics reports rate-limit and
// enumeration counters — precisely the feedback signal a scraper needs to tune
// its request rate to stay under the limit. It must live on the private
// listener only.
func TestMetricsAreNotOnThePublicListener(t *testing.T) {
	public, private := running(t)

	if got := get(t, public, "/metrics").StatusCode; got == http.StatusOK {
		t.Error("/metrics is reachable on the public listener")
	}
	if got := get(t, private, "/metrics").StatusCode; got != http.StatusOK {
		t.Errorf("GET /metrics on the private listener = %d, want 200", got)
	}
}

// TestTilesAreNotOnThePublicListener. This is the test that catches a later
// "simplification" of three listeners back into two. Serving style.json from
// the public listener would put dozens of range requests per map load through
// the 10/s API bucket — and any exemption carved out for them is one routing
// mistake away from covering more than intended.
func TestTilesAreNotOnThePublicListener(t *testing.T) {
	public, private, tiles := runningWithTiles(t, tilesDir(t))

	if got := get(t, tiles, "/style.json").StatusCode; got != http.StatusOK {
		t.Errorf("GET /style.json on the tiles listener = %d, want 200", got)
	}
	if got := get(t, public, "/style.json").StatusCode; got == http.StatusOK {
		t.Error("/style.json is reachable on the public listener")
	}
	if got := get(t, private, "/style.json").StatusCode; got == http.StatusOK {
		t.Error("/style.json is reachable on the private listener")
	}
	// The converse, so a future refactor cannot satisfy this test by pointing
	// all three addresses at one mux that happens to 404 the wrong paths.
	if got := get(t, tiles, "/api/v1/overview").StatusCode; got == http.StatusOK {
		t.Error("the API is reachable on the tiles listener")
	}
	if got := get(t, tiles, "/metrics").StatusCode; got == http.StatusOK {
		t.Error("/metrics is reachable on the tiles listener")
	}
}

// TestTilesListenerIsCapped. The tiles bulkhead separates the pool, the
// snapshot, the limiters and the admission semaphore — but file descriptors and
// goroutines are process-wide and cannot be separated. An uncapped tiles
// listener is therefore a way to exhaust them and take the public listener's
// Accept down with it. And the assumption that makes listen.max_conns look
// redundant on the public port — that the origin is reachable only through
// Cloudflare — is known false here by design: the tiles port sits on a DNS-only
// hostname and accepts the world.
func TestTilesListenerIsCapped(t *testing.T) {
	const maxConns = 2
	_, _, tilesAddr := runningWithTiles(t, tilesDir(t), func(c *config.Config) {
		c.Listen.MaxConns = maxConns
	})

	before := httpx.ConnectionsRejectedCountForTesting()

	// Held open and silent: the cap bounds sockets, not requests, so a
	// connection that never sends a byte must still occupy a slot. That is the
	// whole failure mode — tens of thousands of these complete no request, so
	// no rate limiter or admission cap ever sees them.
	for i := 0; i < maxConns; i++ {
		c, err := net.Dial("tcp", tilesAddr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer c.Close()
	}

	// The listener accepts in FIFO order, so both slots are taken by the time
	// this one is accepted.
	over, err := net.Dial("tcp", tilesAddr)
	if err != nil {
		t.Fatalf("dial over-cap: %v", err)
	}
	defer over.Close()

	// An over-cap connection is accepted from the kernel and closed at once, so
	// this read ends instead of blocking. Two seconds is deliberately well under
	// the 5s ReadHeaderTimeout that would eventually close an ACCEPTED silent
	// connection: a longer deadline would pass with or without the cap.
	_ = over.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := over.Read(make([]byte, 1))
	switch {
	case err == nil:
		t.Fatalf("the over-cap connection read %d bytes and stayed open; the tiles listener has no connection cap", n)
	case errors.Is(err, os.ErrDeadlineExceeded):
		t.Fatalf("the over-cap connection was still open 2s after connecting; the tiles listener has no connection cap")
	}

	if got := httpx.ConnectionsRejectedCountForTesting() - before; got < 1 {
		t.Errorf("airbg_connections_rejected_total rose by %d over the over-cap connection, want at least 1", got)
	}
}

// TestNoTilesStartsTwoListeners. The shipped configuration has no basemap, and
// it must not open a third socket or fail to start.
func TestNoTilesStartsTwoListeners(t *testing.T) {
	public, private := running(t)
	if got := get(t, public, "/").StatusCode; got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	if got := get(t, private, "/healthz").StatusCode; got != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", got)
	}
}

// TestABadTilesDirIsAStartupError. Discovering a mis-set path from a blank map
// in production is the outcome this refuses.
func TestABadTilesDirIsAStartupError(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	holder := snapshot.NewHolder(cfg.Series)
	cfg.Tiles = config.Tiles{
		Addr:      "127.0.0.1:0",
		Dir:       filepath.Join(t.TempDir(), "does-not-exist"),
		PublicURL: "http://127.0.0.1:8082",
	}
	if _, err := server.New(server.Options{Config: cfg, Catalogue: cat, Snapshots: holder}); err == nil {
		t.Fatal("server.New with a missing tiles.dir returned nil error, want an error")
	}
}

func TestSecurityHeadersOnEveryPublicResponse(t *testing.T) {
	public, _ := running(t)

	for _, path := range []string{"/", "/api/v1/overview", "/nope"} {
		h := get(t, public, path).Header
		if h.Get("Content-Security-Policy") == "" {
			t.Errorf("%s has no CSP", path)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s is missing nosniff", path)
		}
	}
}

// TestRequestBodyIsCapped: an unbounded body on a GET-only service is free
// memory pressure for an attacker.
//
// This test only proves that a large POST to a GET-only route does not hang
// and does not return 200 — true of the 405 from the method-qualified route
// regardless of body size, so it does NOT exercise httpx.LimitBody's cap
// itself. It cannot: every route this server serves reads request data from
// query parameters or the in-memory snapshot, never from the body, so the cap
// has no externally observable effect through the real route table (found
// during review to still pass with maxBodyBytes set to 1<<40). The cap is
// pinned directly, at the httpx.LimitBody layer that enforces it, by
// TestMaxBodyBytesConstantIsEnforced in cap_test.go, using the server
// package's actual maxBodyBytes constant in front of a handler that reads the
// body. This test stays as a smoke test for the 405/no-hang behaviour only.
func TestRequestBodyIsCapped(t *testing.T) {
	public, _ := running(t)

	resp, err := http.Post("http://"+public+"/api/v1/overview", "application/json",
		strings.NewReader(strings.Repeat("x", 4<<20)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	// 405 from the method-qualified route is the expected answer; what must NOT
	// happen is a 200 or a hang.
	if resp.StatusCode == http.StatusOK {
		t.Errorf("a 4 MiB POST to a GET route returned 200")
	}
}

// TestReadHeaderTimeoutIsSet is asserted by behaviour, not by reading the
// struct: a connection that opens and sends nothing must be closed by the
// server. Without ReadHeaderTimeout, a few thousand such connections exhaust
// the listener with no traffic at all (slowloris).
func TestSlowClientIsDisconnected(t *testing.T) {
	public, _ := running(t)

	conn, err := net.Dial("tcp", public)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	start := time.Now()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\n")) // deliberately unfinished
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))

	_, err = io.ReadAll(conn)
	elapsed := time.Since(start)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("the server did not close an idle half-open request: %v", err)
		return
	}
	// A clean EOF means the server closed the connection, which is the pass
	// condition — but net/http falls back ReadHeaderTimeout to ReadTimeout
	// (10s here) when ReadHeaderTimeout is unset, so a bound this loose would
	// pass even with ReadHeaderTimeout deleted from the server config. Pinning
	// the elapsed time below readTimeout (10s) forces the close to have come
	// from readHeaderTimeout (5s) specifically, not from that fallback.
	if elapsed >= 8*time.Second {
		t.Errorf("connection stayed open for %v; ReadHeaderTimeout (5s) does not "+
			"appear to be in effect (only the looser ReadTimeout backstop fired)", elapsed)
	}
}

func TestHealthzOnPrivateListener(t *testing.T) {
	_, private := running(t)

	if got := get(t, private, "/healthz").StatusCode; got != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", got)
	}
}

// blockingSeriesStore is an api.DataSource whose AreaSeries call reports it
// has started (so the test knows the request is holding the admission slot),
// then blocks until the test releases it. AreaAtPoint and SensorSeries are
// unused by the request this test drives and are not expected to be called.
type blockingSeriesStore struct {
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

func (b *blockingSeriesStore) AreaAtPoint(ctx context.Context, lon, lat float64) (string, error) {
	return "", errors.New("blockingSeriesStore: AreaAtPoint unexpectedly called")
}

func (b *blockingSeriesStore) SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]store.Point, error) {
	return nil, errors.New("blockingSeriesStore: SensorSeries unexpectedly called")
}

func (b *blockingSeriesStore) AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]store.Point, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.release
	return []store.Point{}, nil
}

// TestSeriesAdmissionCapComesFromConfiguredMaxInflight proves
// Config.Database.MaxInflight — not a package constant — is the size of the
// admission semaphore server.New builds in front of the database-backed
// series routes. With MaxInflight set to 1, one in-flight /series request
// must occupy the only slot and force a concurrent second request to 503,
// exactly the shape internal/api/series_test.go's
// TestSeriesRefusesWhenAdmissionIsFull already pins at the Deps level — this
// is the same property proven through the real server.New wiring, which is
// what actually reads Config.Database.MaxInflight.
func TestSeriesAdmissionCapComesFromConfiguredMaxInflight(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	cfg := testConfig(t)
	cfg.Database.MaxInflight = 1

	holder := snapshot.NewHolder(cfg.Series)
	holder.Store(&snapshot.Snapshot{
		GeneratedAt: time.Now().UTC(),
		KnownSlugs:  map[string]snapshot.AreaMeta{"sofia": {Slug: "sofia"}},
		AreaSeries:  map[string]snapshot.Body{},
	})

	public, private := free(t), free(t)
	cfg.Listen.Addr = public
	cfg.Listen.MetricsAddr = private
	cfg.Listen.BaseURL = "http://" + public

	st := &blockingSeriesStore{started: make(chan struct{}), release: make(chan struct{})}

	srv, err := server.New(server.Options{
		Config: cfg, Catalogue: cat, Snapshots: holder, Store: st,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return within 10s of cancellation")
		}
	})
	waitReady(t, private)

	// period=7d is not the default combination ("24h"), and AreaSeries is
	// empty for "sofia", so this request cannot be served from the snapshot
	// and must reach d.Store.AreaSeries through the admission semaphore.
	const path = "/api/v1/area/sofia/series?metric=P2&period=7d"

	firstErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + public + path)
		if err != nil {
			firstErr <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			firstErr <- fmt.Errorf("first (blocking) request status = %d, want 200", resp.StatusCode)
			return
		}
		firstErr <- nil
	}()

	select {
	case <-st.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never reached AreaSeries; it never occupied the admission slot")
	}

	// A bounded client, not the get(t, ...) helper: if the admission cap were
	// ever wider than 1, this second request would also reach the blocking
	// store and hang until the test's own release below — a plain http.Get
	// would then block for the test binary's full timeout instead of failing
	// with a message that names what went wrong.
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + public + path)
	if err != nil {
		t.Errorf("second concurrent request: %v (admission did not reject it within 3s; "+
			"Database.MaxInflight = 1 means the one slot was already held)", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("second concurrent request status = %d, want 503 "+
				"(Database.MaxInflight = 1 means the one slot was already held)", resp.StatusCode)
		}
	}

	close(st.release)
	if err := <-firstErr; err != nil {
		t.Errorf("first (blocking) request: %v", err)
	}
}

// scrapeExposition fetches the raw Prometheus text exposition from the
// private listener. Parsed with plain string operations, deliberately: this
// project adds no new dependency, and pulling in prometheus/common's expfmt
// parser just to check a handful of lines here would be exactly that.
func scrapeExposition(t *testing.T, private string) string {
	t.Helper()
	resp := get(t, private, "/metrics")
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading /metrics body: %v", err)
	}
	return string(b)
}

// metaLineCounts counts "# HELP <name> ..." and "# TYPE <name> ..." lines per
// metric name. A well-formed exposition has exactly one of each per name; more
// than one means two different metric families are colliding under one name,
// which is not valid Prometheus text format — promtool and OpenMetrics-strict
// parsers reject the whole body, and Prometheus's lenient parser still ends up
// with self-contradictory metadata (and, upstream of parsing, double-counted
// requests, since both families would be incremented on every request).
func metaLineCounts(body string) (help, typ map[string]int) {
	help, typ = map[string]int{}, map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.SplitN(line, " ", 4)
		if len(fields) < 3 {
			continue
		}
		switch {
		case fields[0] == "#" && fields[1] == "HELP":
			help[fields[2]]++
		case fields[0] == "#" && fields[1] == "TYPE":
			typ[fields[2]]++
		}
	}
	return help, typ
}

// vecLabelCounts parses `name{label="value"} count` lines for one metric name
// and returns the summed count per distinct label value.
func vecLabelCounts(t *testing.T, body, metricName string) map[string]int {
	t.Helper()
	prefix := metricName + "{"
	counts := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		eq := strings.Index(rest, `="`)
		closeQuote := strings.LastIndex(rest, `"}`)
		if eq < 0 || closeQuote < 0 || closeQuote < eq {
			t.Fatalf("could not parse exposition line %q", line)
		}
		value := rest[eq+2 : closeQuote]
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\n`, "\n")
		value = strings.ReplaceAll(value, `\\`, `\`)
		countStr := strings.TrimSpace(rest[closeQuote+2:])
		n, err := strconv.Atoi(countStr)
		if err != nil {
			t.Fatalf("could not parse count in exposition line %q: %v", line, err)
		}
		counts[value] += n
	}
	return counts
}

// TestExpositionHasNoDuplicateMetricFamilies guards against two counters
// being registered under the same Prometheus name (as internal/metrics's
// httpRequests and internal/httpx's now-removed requestsTotal briefly were,
// with different label names and help text). Grepping for the name's mere
// presence would not have caught that; only counting the HELP/TYPE blocks
// does.
//
// The property is general — EVERY family in the exposition must declare itself
// once — so every name in the parsed maps is checked, not just the one that
// happened to be duplicated. Naming a single metric would leave the next
// collision, under any other name, entirely unguarded.
func TestExpositionHasNoDuplicateMetricFamilies(t *testing.T) {
	_, private := running(t)
	body := scrapeExposition(t, private)

	help, typ := metaLineCounts(body)

	// The exposition is not empty — otherwise a scrape that returned nothing at
	// all would satisfy every loop below by vacuum.
	if len(help) == 0 || len(typ) == 0 {
		t.Fatalf("exposition declared %d HELP and %d TYPE families, want at least one of each; "+
			"an empty scrape makes the checks below vacuous", len(help), len(typ))
	}

	for name, got := range help {
		if got != 1 {
			t.Errorf("%d %q lines for %s, want exactly 1 — duplicate families under one "+
				"name is invalid exposition format", got, "# HELP", name)
		}
	}
	for name, got := range typ {
		if got != 1 {
			t.Errorf("%d %q lines for %s, want exactly 1", got, "# TYPE", name)
		}
	}

	// A family declaring TYPE without HELP (or the reverse) is the other way
	// this can go wrong, and it is free to check while the maps are open.
	for name := range typ {
		if _, ok := help[name]; !ok {
			t.Errorf("%s has a %q line but no %q line", name, "# TYPE", "# HELP")
		}
	}
	for name := range help {
		if _, ok := typ[name]; !ok {
			t.Errorf("%s has a %q line but no %q line", name, "# HELP", "# TYPE")
		}
	}
}

// TestRouteLabelCardinalityIsBoundedByPattern pins the single most
// consequential property in this package: labelling by r.URL.Path instead of
// r.Pattern would let any unauthenticated caller mint an unbounded number of
// metric label values by looping over random URLs — turning the metrics meant
// to detect an extraction attack into the memory-exhaustion attack itself.
//
// Distinct nonexistent paths are sent under /api/, where the mux genuinely
// finds no matching pattern (unlike the web tree, whose own catch-all "/"
// route absorbs unknown paths under a real, bounded pattern). Each of those
// must collapse onto the single fixed "unmatched" sentinel, not mint its own
// label.
func TestRouteLabelCardinalityIsBoundedByPattern(t *testing.T) {
	public, private := running(t)

	// The counters this reads are process-global (internal/metrics registers
	// them once at package init, shared by every server.New in this test
	// binary), so other tests in this package have already added counts under
	// labels like "unmatched" and "GET /api/v1/overview" by the time this test
	// runs. Comparing a before/after DELTA — rather than the absolute count —
	// isolates what THIS test's requests actually did to the label set.
	before := vecLabelCounts(t, scrapeExposition(t, private), "airbg_http_requests_total")

	get(t, public, "/api/v1/overview")
	get(t, public, "/")

	nonexistent := []string{"/api/v1/nope-1", "/api/v1/nope-2", "/api/v1/nope-3"}
	for _, p := range nonexistent {
		get(t, public, p)
	}

	after := vecLabelCounts(t, scrapeExposition(t, private), "airbg_http_requests_total")
	delta := map[string]int{}
	for label, n := range after {
		if d := n - before[label]; d != 0 {
			delta[label] = d
		}
	}

	if got := delta["unmatched"]; got != len(nonexistent) {
		t.Errorf("unmatched delta = %d, want %d (one count per distinct nonexistent "+
			"path, all absorbed by the single sentinel label); full delta: %v",
			got, len(nonexistent), delta)
	}

	for label := range delta {
		for _, p := range nonexistent {
			if strings.Contains(label, p) {
				t.Errorf("route label %q leaks the request path %q; labelling by path "+
					"lets any caller grow this map without bound", label, p)
			}
		}
	}

	// This test's own five requests can only have touched three labels: the
	// two matched patterns for the real routes hit, plus the one sentinel —
	// no matter how many distinct nonexistent paths were requested. A fourth
	// label appearing in the delta means cardinality grew with the number of
	// distinct requests instead of staying bounded by the route table.
	const wantMaxLabels = 3
	if len(delta) > wantMaxLabels {
		t.Errorf("this test's requests touched %d distinct route labels (%v), want at "+
			"most %d", len(delta), delta, wantMaxLabels)
	}
}
