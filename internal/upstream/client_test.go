package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormaliseSelectsCanonicalMetrics(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, skipped, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	// 2 from sensor 12345 (P1, P2 — durP1 and signal dropped),
	// 3 from sensor 12346. Sensor 12347 has no coordinates.
	if len(readings) != 5 {
		t.Fatalf("len(readings) = %d, want 5", len(readings))
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}

	for _, r := range readings {
		if !IsCanonicalMetric(r.Metric) {
			t.Errorf("non-canonical metric %q survived normalisation", r.Metric)
		}
	}
}

func TestNormaliseCoordinateOrder(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for _, r := range readings {
		if r.Lon < 22 || r.Lon > 29 {
			t.Errorf("sensor %d Lon = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lon)
		}
		if r.Lat < 41 || r.Lat > 45 {
			t.Errorf("sensor %d Lat = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lat)
		}
	}
}

func TestNormaliseParsesTimestamp(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if !readings[0].Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", readings[0].Timestamp, want)
	}
}

func TestNormaliseRejectsGarbage(t *testing.T) {
	if _, _, err := Normalise([]byte("not json")); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestFetchHappyPath(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	want, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	c := New(srv.URL, 5*time.Second)
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("got[0] = %+v, want %+v", got[0], want[0])
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error on non-200 status, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to mention status 500", err.Error())
	}
}

func TestFetchMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := New(srv.URL, 5*time.Second)
	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error on malformed body, got nil")
	}
}
