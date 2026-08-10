package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/httpx"
	"airbg.org/internal/ratelimit"
)

// TestMaxBodyBytesConstantIsEnforced pins the actual cap New() wires into the
// public server's middleware chain.
//
// TestRequestBodyIsCapped in server_test.go only proves a POST to a GET-only
// route returns something other than 200 — true regardless of body size,
// since none of this service's routes read a request body (Phase 1 §7: every
// response but /locate comes from the in-memory snapshot; /locate reads query
// parameters, not the body). So the cap has no externally observable effect
// through the real route table, and a test built on those routes cannot pin
// it — it was found to still pass with maxBodyBytes set to 1<<40.
//
// The cap is only observable at the layer that enforces it: httpx.LimitBody
// wrapping the body in an http.MaxBytesReader. This test builds the SAME
// Chain construction New() uses, with the SAME maxBodyBytes constant, in
// front of a handler that actually reads the body — so a change to
// maxBodyBytes (including deleting it or setting it absurdly high) is exactly
// what this test pins.
func TestMaxBodyBytesConstantIsEnforced(t *testing.T) {
	resolver, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	limiter := ratelimit.New(ratelimit.Rate{PerSecond: 1000, Burst: 1000}, time.Minute)

	var readErr error
	chain := httpx.Chain{Resolver: resolver, Limiter: limiter, MaxBodyBytes: maxBodyBytes}
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	// probeSize is fixed, not maxBodyBytes+1: it must stay comfortably above
	// the real cap (64 KiB) while staying small enough to allocate instantly
	// and never hang, even if maxBodyBytes has been mutated to something
	// absurd (the reviewer's 1<<40 case) — a literal body sized to the cap
	// itself would try to allocate a terabyte in that case instead of failing
	// the assertion below.
	const probeSize = 10 << 20 // 10 MiB
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", probeSize)))
	req.RemoteAddr = "127.0.0.1:12345"
	h.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Fatalf("reading a %d-byte body under a %d-byte cap produced no error; "+
			"the cap is not being enforced", probeSize, maxBodyBytes)
	}
}
