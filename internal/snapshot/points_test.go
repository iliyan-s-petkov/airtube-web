package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/store"
)

func decodeHexes(t *testing.T, b Body) hexPayload {
	t.Helper()
	var p hexPayload
	if err := json.Unmarshal(b.JSON, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return p
}

func pointSnapshot(sensors ...store.SensorReading) *Snapshot {
	return &Snapshot{GeneratedAt: time.Now(), points: pointsFrom(sensors)}
}

// The point tier's whole purpose: an entry is a device and says which one.
func TestPointBodyNamesEachSensor(t *testing.T) {
	s := pointSnapshot(
		sensorAt(77, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(12, 23.3260, 42.7001, map[string]float64{"P1": 30}),
	)
	p := pointBody(t, s, BBox{W: 23, S: 42, E: 24, N: 43})

	if p.ResolutionKM != PointResolutionKM {
		t.Errorf("resolution_km = %v, want %v", p.ResolutionKM, PointResolutionKM)
	}
	if len(p.Hexes) != 2 {
		t.Fatalf("want 2 points, got %d", len(p.Hexes))
	}
	// Ordered by sensor id, not by the order they arrived, so the payload and
	// its ETag are a function of the readings alone.
	if p.Hexes[0].SensorID != 12 || p.Hexes[1].SensorID != 77 {
		t.Errorf("ids = %d, %d; want 12, 77 in that order",
			p.Hexes[0].SensorID, p.Hexes[1].SensorID)
	}
	if p.Hexes[0].N != 1 {
		t.Errorf("n = %d, want 1 — a point is a cell of one sensor", p.Hexes[0].N)
	}
}

// The coordinate on the wire must be the one upstream served. sensor.community
// already fuzzes it; the decision was to republish what is public, not to
// sharpen or to coarsen it.
func TestPointBodyPassesCoordinatesThroughUnchanged(t *testing.T) {
	// Deliberately finer than round4 would leave it: if anything rounds, this
	// value moves.
	const lon, lat = 23.321987654, 42.697612345
	s := pointSnapshot(sensorAt(1, lon, lat, map[string]float64{"P1": 20}))
	p := pointBody(t, s, BBox{W: 23, S: 42, E: 24, N: 43})

	if p.Hexes[0].Lon != lon || p.Hexes[0].Lat != lat {
		t.Errorf("got %v,%v want %v,%v — coordinates must pass through untouched",
			p.Hexes[0].Lon, p.Hexes[0].Lat, lon, lat)
	}
}

// The box is what keeps the tier a map view rather than a national registry, so
// it has to actually exclude.
func TestPointBodyClipsToTheBox(t *testing.T) {
	s := pointSnapshot(
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}), // Sofia
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}), // Varna
	)
	p := pointBody(t, s, BBox{W: 23, S: 42, E: 24, N: 43})

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 point inside the box, got %d", len(p.Hexes))
	}
	if p.Hexes[0].SensorID != 1 {
		t.Errorf("kept sensor %d, want the one inside the box", p.Hexes[0].SensorID)
	}
}

// A bin must never carry an id: there is no single device to name, and a 0
// would read as one rather than as "not applicable".
func TestBinnedHexesCarryNoSensorID(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(77, 23.3219, 42.6977, map[string]float64{"P1": 20}),
	}, HexResolutionKM)
	b, err := encode(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if body := string(b.JSON); strings.Contains(body, "sensor_id") {
		t.Errorf("aggregate payload names a sensor: %s", body)
	}
}

func pointBody(t *testing.T, s *Snapshot, bb BBox) hexPayload {
	t.Helper()
	b, err := s.PointBody(bb)
	if err != nil {
		t.Fatalf("PointBody: %v", err)
	}
	return decodeHexes(t, b)
}

// A single outlier is the reason the statistic changed. Three sensors at 20 and
// one failing towards its ceiling: the median holds at 20, the mean it replaced
// would have reported 265 and recoloured the cell.
func TestBinReportsMedianNotMean(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 23.3220, 42.6978, map[string]float64{"P1": 20}),
		sensorAt(3, 23.3221, 42.6979, map[string]float64{"P1": 20}),
		sensorAt(4, 23.3222, 42.6980, map[string]float64{"P1": 1000}),
	}, HexResolutionKM)

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	if got := p.Hexes[0].Values["P1"]; got != 20 {
		t.Errorf("P1 = %v, want 20 (the median; the mean would be 265)", got)
	}
}

// An even bin averages the two middle values. Taking either one instead would
// make a bin of two sensors report one device's reading while claiming to
// summarise both.
func TestMedianOfAnEvenBinAveragesTheMiddlePair(t *testing.T) {
	if got := median([]float64{10, 20, 30, 100}); got != 25 {
		t.Errorf("median = %v, want 25", got)
	}
}

// The bin's own record of what it saw must survive being asked for a summary.
func TestMedianDoesNotReorderItsInput(t *testing.T) {
	in := []float64{30, 10, 20}
	median(in)
	if in[0] != 30 || in[1] != 10 || in[2] != 20 {
		t.Errorf("input reordered to %v", in)
	}
}
