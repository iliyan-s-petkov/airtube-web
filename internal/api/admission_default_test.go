package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"airbg.org/internal/admit"
	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
	"airbg.org/internal/store"
)

// blockingSource holds every AreaAtPoint call inside the query until gate is
// closed, so a test can put a known number of requests in flight at the same
// instant. Admission is a concurrency bound, and a sequential test cannot
// observe one at all.
type blockingSource struct {
	gate    chan struct{}
	entered chan struct{}
	calls   atomic.Int64
}

func (s *blockingSource) AreaAtPoint(_ context.Context, _, _ float64) (string, error) {
	s.calls.Add(1)
	s.entered <- struct{}{}
	<-s.gate
	return "", nil
}

func (s *blockingSource) SensorSeries(_ context.Context, _ int64, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return nil, nil
}

func (s *blockingSource) AreaSeries(_ context.Context, _, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return nil, nil
}

// trustedLocateHandler builds ONE router, wrapped so the Cloudflare headers are
// honoured. Built once rather than per request because the sharing test needs
// two distinct routers and must control exactly how many exist.
func trustedLocateHandler(t *testing.T, d api.Deps) http.Handler {
	t.Helper()
	res, err := httpx.NewIPResolver(httpx.DefaultCloudflareCIDRs())
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	return httpx.WithClientIP(api.NewRouter(d), res)
}

func trustedLocateRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-IPLatitude", "42.6977")
	req.Header.Set("CF-IPLongitude", "23.3219")
	return req
}

// waitFor polls until cond holds, failing the test rather than hanging forever.
// Used instead of a fixed sleep: the interesting states here are reached in
// microseconds, and a sleep long enough to be safe would make the suite slow for
// no gain.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNilAdmissionFailsClosed covers the fail-closed default in api.NewRouter.
//
// Both halves are load-bearing and neither was covered before: deleting the nil
// check would leave the database paths completely uncapped, and replacing the
// sync.OnceValue with a fresh semaphore per NewRouter call would give an
// embedder holding several routers a collective cap of N x 16 — in both cases
// the cap silently stops capping, which is worse than having none because
// nothing looks wrong.
func TestNilAdmissionFailsClosed(t *testing.T) {
	// The counter and the default semaphore are both process-global, so these
	// subtests must not run in parallel with each other or with anything else
	// that touches the default. Deltas, never absolute counts.
	t.Run("the process default cap is applied", func(t *testing.T) {
		const overshoot = 4
		src := &blockingSource{
			gate:    make(chan struct{}),
			entered: make(chan struct{}, admit.DefaultSize+overshoot),
		}
		d := deps(t, fixture(t))
		d.Store = src
		// The point of the test: nothing wires an Admission, so NewRouter must
		// substitute the default rather than run unbounded.
		d.Admission = nil
		h := trustedLocateHandler(t, d)

		before := api.AdmissionRejectedCountForTesting("locate")

		// Released from a Cleanup as well as inline, so that a FAILING run — the
		// mutation runs, where more requests are admitted than expected — drains
		// its blocked handlers and reports the failure instead of deadlocking.
		var wg sync.WaitGroup
		release := sync.OnceFunc(func() { close(src.gate) })
		t.Cleanup(func() { release(); wg.Wait() })

		for i := 0; i < admit.DefaultSize+overshoot; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.ServeHTTP(httptest.NewRecorder(), trustedLocateRequest())
			}()
		}

		// Exactly DefaultSize requests must reach the query and stick there; the
		// rest must be refused. With no cap at all every request would enter the
		// query and the refusal count would stay at zero, so this is the
		// assertion that distinguishes "default applied" from "no limit".
		waitFor(t, "the admission pool to fill", func() bool {
			return len(src.entered) == admit.DefaultSize
		})
		waitFor(t, "the over-cap requests to be shed", func() bool {
			return api.AdmissionRejectedCountForTesting("locate")-before == overshoot
		})

		release()
		wg.Wait()

		if got := src.calls.Load(); got != admit.DefaultSize {
			t.Errorf("AreaAtPoint calls = %d, want %d — the default cap admitted the wrong number", got, admit.DefaultSize)
		}
		if got := api.AdmissionRejectedCountForTesting("locate") - before; got != overshoot {
			t.Errorf("admission refusals = %d, want %d", got, overshoot)
		}
	})

	t.Run("two routers share one semaphore", func(t *testing.T) {
		holder := &blockingSource{
			gate:    make(chan struct{}),
			entered: make(chan struct{}, admit.DefaultSize),
		}
		dHolder := deps(t, fixture(t))
		dHolder.Store = holder
		dHolder.Admission = nil

		// A SECOND router, built by a separate NewRouter call with its own Deps.
		// If the default were built per call, this router would have 16 free
		// slots of its own and its request would be admitted.
		//
		// Its gate is pre-closed and its entered channel buffered: this stub must
		// never block, so that a router with a semaphore of its own produces a
		// clean assertion failure below rather than a deadlocked test.
		openGate := make(chan struct{})
		close(openGate)
		second := &blockingSource{gate: openGate, entered: make(chan struct{}, 1)}
		dSecond := deps(t, fixture(t))
		dSecond.Store = second
		dSecond.Admission = nil

		first := trustedLocateHandler(t, dHolder)
		other := trustedLocateHandler(t, dSecond)

		var wg sync.WaitGroup
		release := sync.OnceFunc(func() { close(holder.gate) })
		t.Cleanup(func() { release(); wg.Wait() })

		for i := 0; i < admit.DefaultSize; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				first.ServeHTTP(httptest.NewRecorder(), trustedLocateRequest())
			}()
		}
		waitFor(t, "the first router to occupy every slot", func() bool {
			return len(holder.entered) == admit.DefaultSize
		})

		before := api.AdmissionRejectedCountForTesting("locate")
		other.ServeHTTP(httptest.NewRecorder(), trustedLocateRequest())

		if got := api.AdmissionRejectedCountForTesting("locate") - before; got != 1 {
			t.Errorf("admission refusals on the second router = %d, want 1 — the two routers do not share one semaphore", got)
		}
		if got := second.calls.Load(); got != 0 {
			t.Errorf("second router's AreaAtPoint calls = %d, want 0 — it was admitted against a semaphore of its own", got)
		}

		release()
		wg.Wait()
	})
}
