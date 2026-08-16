package testsupport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/config"
	"airbg.org/internal/i18n"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// FreePort asks the OS for a loopback port and immediately releases it. A
// fixed port would make any suite that uses this fail on a developer machine
// that already runs the app; there is a race between release and the
// server's own bind, but it is the same idiom internal/server's own tests
// already rely on.
func FreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// WaitReady polls the private listener's health endpoint until it answers or
// the deadline passes, so callers never race the server's own goroutine.
func WaitReady(t *testing.T, addr string) {
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

// StartServer assigns the store's sensors into their areas, builds one real
// snapshot from it (the same way the collector's Publisher does after every
// ingest cycle), and starts a full server.Server backed by that store and
// snapshot. It returns the public and private listener addresses once the
// private listener answers /healthz.
//
// This is the one place the full construction path — AssignSensors, holder,
// Publisher, i18n, free ports, server.New, run, graceful shutdown — is
// written. internal/server's own e2e suite and internal/e2e's Playwright
// driver both call this rather than keeping their own copy, because a second,
// divergent copy of the container setup is exactly the failure mode a
// same-process test harness exists to avoid.
//
// configure, when supplied, is applied to the Options after the fields below
// are set and before server.New runs — the seam a caller uses to set a field
// (Options.Config.Listen.CSP, say) without every other caller needing to know
// it exists.
func StartServer(t *testing.T, st *store.Store, cfg config.Config, configure ...func(*server.Options)) (public, private string) {
	t.Helper()
	ctx := context.Background()

	if _, _, err := area.AssignSensors(ctx, st.Pool(), cfg.Database.StatementTimeouts.Assign); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	holder := snapshot.NewHolder(cfg.Series)
	pub := server.NewPublisher(st, holder, log)
	if err := pub.Publish(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	public, private = FreePort(t), FreePort(t)
	cfg.Listen.Addr = public
	cfg.Listen.MetricsAddr = private
	cfg.Listen.BaseURL = "http://" + public

	opts := server.Options{
		Config:    cfg,
		Catalogue: cat, Snapshots: holder, Store: st, Publisher: pub,
		Logger: log,
	}
	for _, fn := range configure {
		fn(&opts)
	}
	srv, err := server.New(opts)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx) }()
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

	WaitReady(t, private)
	return public, private
}
