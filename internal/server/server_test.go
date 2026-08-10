package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
)

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

func running(t *testing.T) (public, private string) {
	t.Helper()

	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	holder := snapshot.NewHolder()
	holder.Store(&snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs:  map[string]snapshot.AreaMeta{},
		Overview:    snapshot.Body{JSON: []byte(`{"areas":[]}`), ETag: `"t"`},
	})

	public, private = free(t), free(t)
	srv, err := server.New(server.Options{
		ListenAddr: public, MetricsAddr: private,
		Catalogue: cat, Snapshots: holder, BaseURL: "http://" + public,
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
	return public, private
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
func TestExpositionHasNoDuplicateMetricFamilies(t *testing.T) {
	_, private := running(t)
	body := scrapeExposition(t, private)

	help, typ := metaLineCounts(body)

	const name = "airbg_http_requests_total"
	if got := help[name]; got != 1 {
		t.Errorf("%d %q lines for %s, want exactly 1 — duplicate families under one "+
			"name is invalid exposition format", got, "# HELP", name)
	}
	if got := typ[name]; got != 1 {
		t.Errorf("%d %q lines for %s, want exactly 1", got, "# TYPE", name)
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
