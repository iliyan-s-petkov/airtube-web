package snapshot

import (
	"encoding/json"
	"testing"
	"time"

	"airbg.org/internal/store"
)

// A real pair as upstream publishes it: one address, two devices, disjoint
// metrics.
func pair() []store.SensorReading {
	return []store.SensorReading{
		{SensorID: 5965, SensorType: "SDS011", Lon: 27.976, Lat: 43.224,
			Values: map[string]float64{"P1": 12, "P2": 7}},
		{SensorID: 5966, SensorType: "BME280", Lon: 27.976, Lat: 43.224,
			Values: map[string]float64{"temperature": 18, "humidity": 60, "pressure": 1012}},
	}
}

func TestStationIDsGroupsTheDevicesAtOneAddress(t *testing.T) {
	got := stationIDs(pair())
	if len(got) != 2 || got[0] != 5965 || got[1] != 5965 {
		t.Fatalf("stationIDs = %v, want both devices under 5965 (the lower member id)", got)
	}
}

// The lower id wins whichever way round the rows arrive: two snapshots of the
// same site must not disagree about what that site is called.
func TestStationIDIsIndependentOfRowOrder(t *testing.T) {
	rows := pair()
	forward := stationIDs(rows)
	reversed := stationIDs([]store.SensorReading{rows[1], rows[0]})
	if forward[0] != reversed[0] {
		t.Errorf("row order changed the station id: %v vs %v", forward, reversed)
	}
}

// A device standing alone is its own station. Anything else would leave the
// 43 single-device sites in the country with no station to belong to.
func TestStationIDOfALoneSensorIsItsOwnID(t *testing.T) {
	got := stationIDs([]store.SensorReading{
		{SensorID: 19774, Lon: 27.9372, Lat: 43.2178, Values: map[string]float64{"P1": 5}},
	})
	if len(got) != 1 || got[0] != 19774 {
		t.Fatalf("stationIDs = %v, want [19774]", got)
	}
}

// Grouping is on the exact published coordinate. Two devices a hundred metres
// apart are two stations, and merging them would put one device's temperature
// on another device's address.
func TestNearbyIsNotTheSameStation(t *testing.T) {
	got := stationIDs([]store.SensorReading{
		{SensorID: 100, Lon: 27.976, Lat: 43.224},
		{SensorID: 200, Lon: 27.977, Lat: 43.224},
	})
	if got[0] == got[1] {
		t.Errorf("stationIDs = %v, want two distinct stations for two distinct coordinates", got)
	}
}

// The column has to reach the wire, and has to line up with the others: the
// frontend joins on index, so a station column of the wrong length would
// attach one device's readings to another device's marker.
func TestSensorPayloadCarriesTheStationColumn(t *testing.T) {
	body, err := json.Marshal(sensorPayloadFrom(time.Unix(1_800_000_000, 0).UTC(), pair()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Sensors struct {
			ID      []int64    `json:"id"`
			Station []int64    `json:"station"`
			P1      []*float64 `json:"P1"`
		} `json:"sensors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if len(got.Sensors.Station) != len(got.Sensors.ID) {
		t.Fatalf("station column has %d entries, id has %d", len(got.Sensors.Station), len(got.Sensors.ID))
	}
	if got.Sensors.Station[0] != 5965 || got.Sensors.Station[1] != 5965 {
		t.Errorf("station = %v, want both entries 5965", got.Sensors.Station)
	}
	// The devices stay separate rows: the wire keeps reporting what each piece
	// of hardware measured, and the station is a join key, not a merge.
	if got.Sensors.P1[1] != nil {
		t.Errorf("the climate device reports P1 = %v; the merge belongs in the client, not in the payload", *got.Sensors.P1[1])
	}
}
