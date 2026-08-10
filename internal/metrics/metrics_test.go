package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"airbg.org/internal/metrics"
)

func TestCounterExposition(t *testing.T) {
	c := metrics.Counter("airbg_test_total", "A test counter.")
	c.Inc()
	c.Add(4)

	body := scrape(t)

	// Prometheus requires the HELP and TYPE lines before the sample. A scraper
	// tolerates their absence, but the metric then has no documentation and no
	// declared type, which changes how it is aggregated.
	for _, want := range []string{
		"# HELP airbg_test_total A test counter.",
		"# TYPE airbg_test_total counter",
		"airbg_test_total 5",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition missing %q\ngot:\n%s", want, body)
		}
	}
}

func TestCounterVecEscapesLabelValues(t *testing.T) {
	v := metrics.CounterVec("airbg_test_labelled_total", "Labelled.", "route")
	// A label value containing a quote, a backslash and a newline. All three
	// must be escaped or the exposition becomes unparseable — and route labels
	// come from request paths, which are attacker-controlled.
	v.With("/a\"b\\c\nd").Inc()

	body := scrape(t)
	if strings.Contains(body, "\nd") && strings.Contains(body, "airbg_test_labelled_total{route=\"/a\"b") {
		t.Errorf("label value was not escaped:\n%s", body)
	}
	if !strings.Contains(body, `airbg_test_labelled_total{route="/a\"b\\c\nd"} 1`) {
		t.Errorf("expected escaped label line, got:\n%s", body)
	}
}

func TestGaugeSet(t *testing.T) {
	g := metrics.Gauge("airbg_test_gauge", "A gauge.")
	g.Set(42.5)

	body := scrape(t)
	if !strings.Contains(body, "# TYPE airbg_test_gauge gauge") {
		t.Errorf("gauge type line missing:\n%s", body)
	}
	if !strings.Contains(body, "airbg_test_gauge 42.5") {
		t.Errorf("gauge value missing:\n%s", body)
	}
}

// TestConcurrentIncIsRaceFree is run under -race. Counters are incremented from
// every request goroutine, so this is the normal case, not an edge case.
func TestConcurrentIncIsRaceFree(t *testing.T) {
	c := metrics.Counter("airbg_test_concurrent_total", "Concurrent.")
	v := metrics.CounterVec("airbg_test_concurrent_labelled_total", "Concurrent labelled.", "k")

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Inc()
				// Distinct label values from concurrent goroutines exercise the
				// map-write path, not just the atomic increment.
				v.With(string(rune('a' + i%4))).Inc()
			}
		}(i)
	}
	wg.Wait()

	if got := c.Value(); got != 3200 {
		t.Errorf("counter = %d, want 3200", got)
	}
}

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
