package snapshot_test

import (
	"encoding/json"
	"testing"
	"time"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// The overlay's whole risk is being read as measurement, so the fields that say
// otherwise are part of the contract, not decoration. See docs/wind-overlay.md.
func TestWindPayloadNamesItselfAForecast(t *testing.T) {
	now := time.Date(2026, 9, 5, 14, 20, 0, 0, time.UTC)
	validAt := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)

	body := snapshot.WindPayloadJSONForTesting(now, validAt, "ecmwf_ifs025", 0.25,
		[]store.WindVector{{Q: 0, R: 0, SpeedMS: 3.46, Direction: 270.4}})

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["forecast"] != true {
		t.Errorf("forecast = %v, want true", got["forecast"])
	}
	if got["model"] != "ecmwf_ifs025" {
		t.Errorf("model = %v, want the model name", got["model"])
	}
	if got["model_resolution_deg"] != 0.25 {
		t.Errorf("model_resolution_deg = %v, want 0.25", got["model_resolution_deg"])
	}
	// The forecast hour is not the build time. A client showing generated_at as
	// the validity would claim the forecast is twenty minutes fresher than it is.
	if got["valid_at"] != "2026-09-05T14:00:00Z" {
		t.Errorf("valid_at = %v, want the forecast hour, not the build time", got["valid_at"])
	}
	if got["generated_at"] != "2026-09-05T14:20:00Z" {
		t.Errorf("generated_at = %v, want the build time", got["generated_at"])
	}
}

func TestWindVectorsCarryTheHexCentre(t *testing.T) {
	now := time.Now().UTC()
	body := snapshot.WindPayloadJSONForTesting(now, now, "m", 0.25,
		[]store.WindVector{{Q: 0, R: 0, SpeedMS: 3.46, Direction: 270.44}})

	var got struct {
		Vectors []struct {
			Lon, Lat, SpeedMS float64
			DirectionDeg      float64 `json:"direction_deg"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Vectors) != 1 {
		t.Fatalf("got %d vectors, want 1", len(got.Vectors))
	}
	// Hex (0,0) is the grid's anchor, which is (0°, 0°) in projected space.
	if got.Vectors[0].Lon != 0 || got.Vectors[0].Lat != 0 {
		t.Errorf("vector at hex (0,0) = %v, %v; want the grid anchor at 0,0",
			got.Vectors[0].Lon, got.Vectors[0].Lat)
	}
	if got.Vectors[0].DirectionDeg != 270.4 {
		t.Errorf("direction_deg = %v, want 270.4 — one decimal is finer than the model",
			got.Vectors[0].DirectionDeg)
	}
}

// The grid is the set of hexes that hold sensors, so several sensors in one hex
// must produce one point: the request URL is built from this, and a duplicate
// costs a location in a batch and stores the same row twice.
func TestHexGridDeduplicatesAndOrders(t *testing.T) {
	sensors := []store.SensorReading{
		{SensorID: 1, Lon: 27.9, Lat: 43.2},
		{SensorID: 2, Lon: 23.3, Lat: 42.7},
		{SensorID: 3, Lon: 23.301, Lat: 42.701}, // same hex as sensor 2
	}

	cells := snapshot.HexGridOf(sensors)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2 — sensors 2 and 3 share a hex", len(cells))
	}
	// Ordered by coordinate, so two identical sensor sets build the same URL.
	if cells[0].Q > cells[1].Q || (cells[0].Q == cells[1].Q && cells[0].R > cells[1].R) {
		t.Errorf("cells are not ordered by coordinate: %+v", cells)
	}
}

func TestHexGridOfNoSensorsIsEmpty(t *testing.T) {
	if cells := snapshot.HexGridOf(nil); len(cells) != 0 {
		t.Errorf("got %d cells for no sensors, want 0", len(cells))
	}
}
