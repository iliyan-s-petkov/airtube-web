package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"airbg.org/internal/metrics"
)

// TestInstrumentKeepsTheWriterReachable. Instrument wraps every response writer
// in the chain, so a wrapper without Unwrap costs each handler its Flush.
func TestInstrumentKeepsTheWriterReachable(t *testing.T) {
	var flushErr error
	h := metrics.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if flushErr != nil {
		t.Errorf("Flush through Instrument = %v, want nil", flushErr)
	}
}
