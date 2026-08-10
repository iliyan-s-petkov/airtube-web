package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
