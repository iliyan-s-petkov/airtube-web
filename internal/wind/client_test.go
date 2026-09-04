package wind_test

import (
	"net/url"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/wind"
)

func points() []wind.Point {
	return []wind.Point{
		{Q: 1, R: 2, Lon: 23.3, Lat: 42.7},
		{Q: 5, R: 9, Lon: 27.9, Lat: 43.2},
	}
}

// The response's coordinates are the model's grid cell, not the ones asked for,
// so the join is positional. Both points here snap to the same cell, which is
// what a coordinate match would get wrong.
func TestResponseIsKeyedByIndexNotCoordinate(t *testing.T) {
	payload := []byte(`[
	  {"latitude":42.75,"longitude":23.25,"hourly":{"time":["2026-09-05T00:00"],"wind_speed_10m":[3.5],"wind_direction_10m":[270]}},
	  {"latitude":42.75,"longitude":23.25,"hourly":{"time":["2026-09-05T00:00"],"wind_speed_10m":[8.1],"wind_direction_10m":[90]}}
	]`)

	got, err := wind.Parse(payload, points())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d forecasts, want 2", len(got))
	}
	if got[0].Q != 1 || got[0].R != 2 || got[0].SpeedMS != 3.5 {
		t.Errorf("first forecast = %+v, want hex (1,2) at 3.5 m/s", got[0])
	}
	if got[1].Q != 5 || got[1].R != 9 || got[1].SpeedMS != 8.1 {
		t.Errorf("second forecast = %+v, want hex (5,9) at 8.1 m/s", got[1])
	}
}

// A short array would otherwise shift every vector onto the wrong hex, which
// draws as a plausible wind field that is simply wrong.
func TestShortResponseIsAnError(t *testing.T) {
	payload := []byte(`[{"latitude":42.75,"longitude":23.25,"hourly":{"time":[],"wind_speed_10m":[],"wind_direction_10m":[]}}]`)

	if _, err := wind.Parse(payload, points()); err == nil {
		t.Fatal("Parse accepted 1 location for 2 points, want an error")
	}
}

func TestRaggedHourlyArraysAreAnError(t *testing.T) {
	payload := []byte(`[
	  {"hourly":{"time":["2026-09-05T00:00","2026-09-05T01:00"],"wind_speed_10m":[3.5],"wind_direction_10m":[270,280]}},
	  {"hourly":{"time":[],"wind_speed_10m":[],"wind_direction_10m":[]}}
	]`)

	if _, err := wind.Parse(payload, points()); err == nil {
		t.Fatal("Parse accepted 2 timestamps against 1 speed, want an error")
	}
}

// A gap in the model run is absent, not calm: stored as zero it would draw a
// still arrow, which is a claim rather than a silence.
func TestNullHoursAreDroppedNotZeroed(t *testing.T) {
	payload := []byte(`[
	  {"hourly":{"time":["2026-09-05T00:00","2026-09-05T01:00"],"wind_speed_10m":[null,4.0],"wind_direction_10m":[270,null]}},
	  {"hourly":{"time":["2026-09-05T00:00"],"wind_speed_10m":[1.0],"wind_direction_10m":[10]}}
	]`)

	got, err := wind.Parse(payload, points())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d forecasts, want 1 — both of the first location's hours are incomplete", len(got))
	}
	if got[0].Q != 5 || got[0].SpeedMS != 1.0 {
		t.Errorf("surviving forecast = %+v, want hex (5,9) at 1.0 m/s", got[0])
	}
}

func TestTimestampsAreParsedAsUTC(t *testing.T) {
	payload := []byte(`[
	  {"hourly":{"time":["2026-09-05T13:00"],"wind_speed_10m":[3.5],"wind_direction_10m":[270]}},
	  {"hourly":{"time":[],"wind_speed_10m":[],"wind_direction_10m":[]}}
	]`)

	got, err := wind.Parse(payload, points())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	if !got[0].ValidAt.Equal(want) {
		t.Errorf("ValidAt = %v, want %v", got[0].ValidAt, want)
	}
	if _, offset := got[0].ValidAt.Zone(); offset != 0 {
		t.Errorf("ValidAt zone offset = %d, want 0; the request asks for UTC", offset)
	}
}

// The request must ask for m/s. Open-Meteo's default is km/h, and the
// difference is a factor of 3.6 in a number nothing else would flag.
func TestRequestAsksForMetresPerSecondAndTheConfiguredModel(t *testing.T) {
	raw := wind.RequestURLForTesting(testConfig(), points())
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	q := u.Query()
	if got := q.Get("wind_speed_unit"); got != "ms" {
		t.Errorf("wind_speed_unit = %q, want \"ms\"", got)
	}
	if got := q.Get("models"); got != "ecmwf_ifs025" {
		t.Errorf("models = %q, want the configured model", got)
	}
	if got := q.Get("timezone"); got != "UTC" {
		t.Errorf("timezone = %q, want \"UTC\"", got)
	}
	// Latitudes and longitudes are parallel lists, and their order is the only
	// thing tying an answer to a hex.
	if got, want := q.Get("latitude"), "42.7000,43.2000"; got != want {
		t.Errorf("latitude = %q, want %q", got, want)
	}
	if got, want := q.Get("longitude"), "23.3000,27.9000"; got != want {
		t.Errorf("longitude = %q, want %q", got, want)
	}
}

func testConfig() config.Wind {
	return config.Wind{
		URL:             "https://api.open-meteo.com/v1/forecast",
		Model:           "ecmwf_ifs025",
		ResolutionDeg:   0.25,
		RequestTimeout:  30 * time.Second,
		PollInterval:    time.Hour,
		ForecastHours:   24,
		PointsPerReq:    100,
		MaxPayloadBytes: 1 << 24,
		Retention:       48 * time.Hour,
	}
}
