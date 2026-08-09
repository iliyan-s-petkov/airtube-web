package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net"
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
	batch, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := batch.Readings

	if len(got) == 0 {
		t.Fatal("Fetch returned no readings from the recorded fixture")
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

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until either the client gives up (request context cancelled)
		// or a generous safety ceiling elapses, whichever comes first — never
		// sleep unconditionally, so this handler cannot outlive the test.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	c := New(srv.URL, 20*time.Millisecond)
	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error on client timeout, got nil")
	}

	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if errors.As(err, &netErr) && netErr.Timeout() {
		return
	}
	t.Errorf("error = %q, want context.DeadlineExceeded or a net.Error with Timeout() true", err.Error())
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

func TestNormaliseConvertsPressureToHectopascals(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	var found bool
	for _, r := range readings {
		if r.Metric != "pressure" {
			continue
		}
		found = true
		if r.Value < 800 || r.Value > 1100 {
			t.Errorf("pressure = %v hPa, outside plausible range — unit not converted", r.Value)
		}
	}
	if !found {
		t.Fatal("no pressure reading in fixture output")
	}
}

// TestNormaliseAcceptsBothValueEncodings covers the live schema drift where
// upstream started sending some sensordatavalues.value fields as a bare JSON
// number instead of the historical quoted string. Sensor 30001 sends P1 as
// "24.30" (string); sensor 30002 sends the same value 24.30 as a number.
// Both must normalise to the identical float64.
func TestNormaliseAcceptsBothValueEncodings(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample_value_types.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	var quoted, unquoted *float64
	for i := range readings {
		r := &readings[i]
		if r.Metric != "P1" {
			continue
		}
		switch r.SensorID {
		case 30001:
			quoted = &r.Value
		case 30002:
			unquoted = &r.Value
		}
	}
	if quoted == nil || unquoted == nil {
		t.Fatalf("expected P1 readings from both sensor 30001 (quoted) and 30002 (unquoted), got readings=%+v", readings)
	}
	if *quoted != *unquoted {
		t.Errorf("quoted-string P1 = %v, unquoted-number P1 = %v, want equal", *quoted, *unquoted)
	}
	if *quoted != 24.30 {
		t.Errorf("P1 = %v, want 24.30", *quoted)
	}
}

// TestNormaliseSkipsSingleBadValueOnly asserts the per-entry tolerance holds
// even when the payload-level unmarshal would previously have failed
// outright: sensor 30005 sends a P2 value that is a JSON object, which is
// neither a numeric string nor a number. That entry alone must be skipped;
// every other entry in the same payload must still come through.
//
// Against the pre-fix code (value typed as a plain Go string) this test
// fails at the Normalise call itself: json.Unmarshal errors out on the
// object-typed value field, so err != nil and the whole payload is rejected
// — not just the one bad entry.
func TestNormaliseSkipsSingleBadValueOnly(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample_value_types.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, skipped, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v, want the object-valued entry to be skipped, not fail the batch", err)
	}

	// 30001 (P1), 30002 (P1), 30003 (pressure), 30004 (temperature only —
	// pressure_at_sealevel is non-canonical and dropped). 30005 contributes
	// nothing: its only value is object-typed and unparseable.
	if len(readings) != 4 {
		t.Errorf("len(readings) = %d, want 4", len(readings))
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1 (sensor 30005, object-typed value)", skipped)
	}
	for _, r := range readings {
		if r.SensorID == 30005 {
			t.Errorf("sensor 30005 should have been skipped entirely, got reading %+v", r)
		}
	}
}

// TestNormaliseIgnoresPressureAtSealevel asserts pressure_at_sealevel is
// dropped as non-canonical (it is not in the seven-metric canonical set) and
// is never confused with "pressure": no reading carries either metric name
// for sensor 30004, and the genuine "pressure" reading from sensor 30003 is
// still converted Pascals -> hPa while pressure_at_sealevel is not converted
// at all (it is simply absent).
func TestNormaliseIgnoresPressureAtSealevel(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample_value_types.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for _, r := range readings {
		if r.Metric == "pressure_at_sealevel" {
			t.Fatalf("pressure_at_sealevel must be dropped as non-canonical, got reading %+v", r)
		}
		if r.SensorID == 30004 && r.Metric != "temperature" {
			t.Errorf("sensor 30004 should only yield its canonical temperature reading, got metric %q", r.Metric)
		}
	}

	var gotPressure bool
	for _, r := range readings {
		if r.SensorID != 30003 || r.Metric != "pressure" {
			continue
		}
		gotPressure = true
		// 94210.00 Pa -> 942.10 hPa.
		if r.Value != 942.1 {
			t.Errorf("pressure = %v hPa, want 942.1 (94210 Pa / 100)", r.Value)
		}
	}
	if !gotPressure {
		t.Fatal("expected a converted pressure reading from sensor 30003")
	}
}

// TestNormaliseSkipsStructurallyBrokenEntriesOnly is Finding 1 from the
// task-14 review: the "one bad entry never fails the batch" guarantee has to
// hold at the level of the whole entry, not just the value field. This
// payload mixes three good entries with two structurally different ones:
// sensor 40003 sends sensor.id as a quoted string instead of a number, and
// sensor 40004 sends sensordatavalues as a JSON object instead of an array.
// Sensor 40002 also exercises latitude/longitude sent as bare JSON numbers
// (rather than the historical quoted string) to prove that tolerance too.
//
// Against the pre-fix code (single json.Unmarshal(payload, &[]apiEntry))
// this fails at the Normalise call itself: any one of the two structural
// mismatches aborts decoding of the entire array, so err != nil and every
// good entry is lost along with the bad ones.
func TestNormaliseSkipsStructurallyBrokenEntriesOnly(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample_entry_drift.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, skipped, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v, want structurally broken entries to be skipped, not fail the batch", err)
	}

	if len(readings) != 3 {
		t.Errorf("len(readings) = %d, want 3 (sensors 40001, 40002, 40005)", len(readings))
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (sensor 40003: id as string; sensor 40004: sensordatavalues as object)", skipped)
	}

	want := map[int64]bool{40001: true, 40002: true, 40005: true}
	for _, r := range readings {
		if !want[r.SensorID] {
			t.Errorf("unexpected sensor %d in output; expected only %v", r.SensorID, want)
		}
		delete(want, r.SensorID)
	}
	if len(want) != 0 {
		t.Errorf("missing readings for sensors %v", want)
	}

	// Sensor 40002 sent latitude/longitude as bare numbers; confirm they
	// parsed to the expected coordinates, not zero or an error swallowed
	// silently.
	for _, r := range readings {
		if r.SensorID != 40002 {
			continue
		}
		if r.Lat != 42.1354 || r.Lon != 24.7453 {
			t.Errorf("sensor 40002 coords = (%v, %v), want (24.7453, 42.1354)", r.Lon, r.Lat)
		}
	}
}

// TestParseValueRejectsNull pins Minor 4 from the task-14 review: JSON null
// must be rejected as an unparseable value, not silently normalised to 0.0.
// json.Unmarshal([]byte("null"), &s) is a documented no-op that leaves s
// untouched (empty string) and returns a nil error, so today this falls
// through to strconv.ParseFloat("") and correctly errors — but only because
// of the specific branch order in parseValue. A reordering (e.g. trying the
// float64 branch first) would make json.Unmarshal([]byte("null"), &f) a
// no-op returning nil error too, silently yielding 0.0. This test exists so
// that regression is caught explicitly rather than by accident.
func TestParseValueRejectsNull(t *testing.T) {
	_, err := parseValue(json.RawMessage("null"))
	if err == nil {
		t.Fatal("parseValue(null) = nil error, want an error — null is not a valid reading")
	}
}
