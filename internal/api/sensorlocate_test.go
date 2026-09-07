package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/snapshot"
)

func locateFixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	snap := fixture(t)
	snap.SensorLocations = map[int64]snapshot.SensorLocation{
		11338: {SensorID: 11338, Lon: 23.31, Lat: 42.69, Slug: "sofia"},
	}
	return snap
}

func sensorLocate(t *testing.T, d api.Deps, id string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	api.NewRouter(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sensor/"+id+"/locate", nil))
	return rec
}

// The deep link is the only thing a page opened on /en/#sensor=11338 has, so
// this endpoint is what turns it into a map view.
func TestSensorLocateAnswersFromTheSnapshot(t *testing.T) {
	rec := sensorLocate(t, deps(t, locateFixture(t)), "11338")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got snapshot.SensorLocation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := snapshot.SensorLocation{SensorID: 11338, Lon: 23.31, Lat: 42.69, Slug: "sofia"}
	if got != want {
		t.Errorf("body = %+v, want %+v", got, want)
	}

	// Never public: the response is keyed by sensor id, and a shared cache
	// holding it would serve exactly the enumeration the breadth check meters.
	if cc := rec.Header().Get("Cache-Control"); cc == "" || cc[:7] != "private" {
		t.Errorf("Cache-Control = %q, want private", cc)
	}
}

func TestSensorLocateRejectsAnUnknownOrMalformedID(t *testing.T) {
	d := deps(t, locateFixture(t))
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"999999", http.StatusNotFound},
		{"abc", http.StatusBadRequest},
		{"0", http.StatusBadRequest},
		{"-3", http.StatusBadRequest},
	} {
		if got := sensorLocate(t, d, tc.id).Code; got != tc.want {
			t.Errorf("id %q: status = %d, want %d", tc.id, got, tc.want)
		}
	}
}

// The same breadth budget as the series endpoint, and for the same reason: a
// caller who can ask for one sensor's position can ask for every sensor's.
func TestSensorLocateIsMeteredByEnumerationBreadth(t *testing.T) {
	d := deps(t, locateFixture(t))
	limit := d.Config.RateLimit.Enumerate.SensorsPerWindow

	var last *httptest.ResponseRecorder
	for i := 0; i <= limit; i++ {
		last = sensorLocate(t, d, fmt.Sprintf("%d", 100000+i))
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d distinct sensors = %d, want 429", limit+1, last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("Retry-After missing on the refusal")
	}
}
