package snapshot

import (
	"encoding/json"
	"testing"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// A device measures what its hardware measures, whether or not this cycle's
// reading survived the quality filter. Without that, a rejected reading is
// indistinguishable from a thermometer the box does not have, and the panel has
// to print "no reading" for all seven metrics on every sensor in the country.
func TestMeasuresKeepsAMetricWhoseReadingWasRejected(t *testing.T) {
	sr := store.SensorReading{
		SensorID: 5966, SensorType: "BME280",
		Values:   map[string]float64{"temperature": 18, "humidity": 60},
		Measures: []string{"humidity", "pressure", "temperature"},
	}
	got := measuresOf(sr, upstream.CanonicalMetrics())
	if want := []string{"humidity", "pressure", "temperature"}; !equal(got, want) {
		t.Errorf("measuresOf = %v, want %v — pressure is measured here, its reading was just unusable", got, want)
	}
}

// The metrics this hardware has nothing to say about stay out, which is the
// whole point: they are the rows that should not be on screen at all.
func TestMeasuresLeavesOutWhatTheDeviceNeverReported(t *testing.T) {
	sr := store.SensorReading{
		SensorID: 5965, SensorType: "SDS011",
		Values:   map[string]float64{"P1": 12, "P2": 7},
		Measures: []string{"P1", "P2"},
	}
	got := measuresOf(sr, upstream.CanonicalMetrics())
	for _, m := range got {
		if m != "P1" && m != "P2" {
			t.Errorf("measuresOf = %v, want P1 and P2 only; an SDS011 has no %s", got, m)
		}
	}
}

// Canonical order, not the store's: the client walks the metric list the server
// published, and a set that arrives in a different order for every sensor is a
// payload that gzips worse and reads worse for no gain.
func TestMeasuresFollowsTheCanonicalOrder(t *testing.T) {
	sr := store.SensorReading{Measures: []string{"temperature", "P2", "humidity", "P1"}}
	got := measuresOf(sr, upstream.CanonicalMetrics())
	if want := []string{"P1", "P2", "humidity", "temperature"}; !equal(got, want) {
		t.Errorf("measuresOf = %v, want %v", got, want)
	}
}

// A metric outside the canonical set has no column in the payload, so promising
// it here would name a row the client can never fill.
func TestMeasuresDropsWhatHasNoColumn(t *testing.T) {
	sr := store.SensorReading{Measures: []string{"P1", "signal", "durP1"}}
	if got := measuresOf(sr, upstream.CanonicalMetrics()); !equal(got, []string{"P1"}) {
		t.Errorf("measuresOf = %v, want [P1]", got)
	}
}

// A row from before the column existed still has values. Falling back to those
// keeps the panel showing every reading it has rather than none of them.
func TestMeasuresFallsBackToTheValuesItHas(t *testing.T) {
	sr := store.SensorReading{Values: map[string]float64{"P2": 7}}
	if got := measuresOf(sr, upstream.CanonicalMetrics()); !equal(got, []string{"P2"}) {
		t.Errorf("measuresOf = %v, want [P2]", got)
	}
}

// The column has to reach the wire and line up with the others: the frontend
// joins on index, so a short measures column would tell one device's panel what
// another device measures.
func TestSensorPayloadCarriesTheMeasuresColumn(t *testing.T) {
	sensors := pair()
	sensors[0].Measures = []string{"P1", "P2"}
	sensors[1].Measures = []string{"humidity", "pressure", "temperature"}

	body, err := json.Marshal(sensorPayloadFrom(time.Unix(1_800_000_000, 0).UTC(), sensors))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Sensors struct {
			ID       []int64    `json:"id"`
			Measures [][]string `json:"measures"`
		} `json:"sensors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if len(got.Sensors.Measures) != len(got.Sensors.ID) {
		t.Fatalf("measures has %d entries, id has %d", len(got.Sensors.Measures), len(got.Sensors.ID))
	}
	if !equal(got.Sensors.Measures[0], []string{"P1", "P2"}) {
		t.Errorf("the particulate device measures %v, want [P1 P2]", got.Sensors.Measures[0])
	}
	if !equal(got.Sensors.Measures[1], []string{"humidity", "pressure", "temperature"}) {
		t.Errorf("the climate device measures %v, want humidity, pressure and temperature", got.Sensors.Measures[1])
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
