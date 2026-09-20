package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/store"
)

// aggFrom is one covered area with a blended values map and a breakdown.
func aggFrom(slug, kind string, stations int, values map[string]float64, by map[string]store.SourceAggregate) store.AreaAggregate {
	return store.AreaAggregate{
		Slug: slug, Kind: kind, NameBG: slug, NameEN: slug,
		CentroidLon: 23.3, CentroidLat: 42.7, DefaultZoom: 9,
		SensorCount: stations, Covered: true, Values: values, BySource: by,
	}
}

func entryFor(t *testing.T, aggs []store.AreaAggregate, slug string) areaPayloadEntry {
	t.Helper()
	for _, e := range areaPayloadFrom(time.Now(), aggs).Areas {
		if e.Slug == slug {
			return e
		}
	}
	t.Fatalf("no entry for %q", slug)
	return areaPayloadEntry{}
}

func TestMixedAreaReportsEachNetworkSeparately(t *testing.T) {
	e := entryFor(t, []store.AreaAggregate{
		aggFrom("sofia", "oblast", 4, map[string]float64{"P2": 25},
			map[string]store.SourceAggregate{
				"sensor.community": {N: 3, Values: map[string]float64{"P2": 20}},
				"eea":              {N: 1, Values: map[string]float64{"P2": 100}},
			}),
	}, "sofia")

	if e.Source != "" {
		t.Errorf("source = %q, want empty on a two-network area", e.Source)
	}
	sc, ok := e.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", e.BySource)
	}
	if sc.N != 3 || sc.Values["P2"] != 20 {
		t.Errorf("sensor.community = {n:%d P2:%v}, want {n:3 P2:20}", sc.N, sc.Values["P2"])
	}
	eea := e.BySource["eea"]
	if eea.N != 1 || eea.Values["P2"] != 100 {
		t.Errorf("eea = {n:%d P2:%v}, want {n:1 P2:100}", eea.N, eea.Values["P2"])
	}
	if e.Values["P2"] != 25 {
		t.Errorf("values.P2 = %v, want the unchanged blended 25", e.Values["P2"])
	}
	if sc.Values["P2"] == e.Values["P2"] || eea.Values["P2"] == e.Values["P2"] {
		t.Error("a per-source median equals the blended one; values was copied")
	}
}

func TestSingleNetworkAreaNamesItsSourceAndOmitsBySource(t *testing.T) {
	e := entryFor(t, []store.AreaAggregate{
		aggFrom("plovdiv", "city", 3, map[string]float64{"P2": 20},
			map[string]store.SourceAggregate{
				"sensor.community": {N: 3, Values: map[string]float64{"P2": 20}},
			}),
	}, "plovdiv")

	if e.Source != "sensor.community" {
		t.Errorf("source = %q, want \"sensor.community\"", e.Source)
	}
	if e.BySource != nil {
		t.Errorf("by_source = %#v, want nil on a one-network area", e.BySource)
	}
}

// The published body for a citizen-only area is byte-identical to the one this
// change replaces: `values` is still the blend, which for one network IS that
// network's median, and the only added key is the scalar `source`.
func TestSingleNetworkAreaValuesAreThePublishedOnes(t *testing.T) {
	blended := map[string]float64{"P2": 20, "P1": 40}
	agg := aggFrom("plovdiv", "city", 3, blended,
		map[string]store.SourceAggregate{
			"sensor.community": {N: 3, Values: map[string]float64{"P2": 20, "P1": 40}},
		})

	e := entryFor(t, []store.AreaAggregate{agg}, "plovdiv")
	if len(e.Values) != len(blended) {
		t.Fatalf("values = %#v, want the unchanged %#v", e.Values, blended)
	}
	for m, v := range blended {
		if e.Values[m] != v {
			t.Errorf("values[%s] = %v, want %v", m, e.Values[m], v)
		}
	}

	raw, err := json.Marshal(areaPayloadFrom(time.Now(), []store.AreaAggregate{agg}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if body := string(raw); strings.Contains(body, "by_source") {
		t.Errorf("the body carries by_source for a one-network area: %s", body)
	}
}

func TestUncoveredAreaPublishesNoBreakdown(t *testing.T) {
	a := aggFrom("vidin", "oblast", 2, map[string]float64{}, map[string]store.SourceAggregate{})
	a.Covered = false
	e := entryFor(t, []store.AreaAggregate{a}, "vidin")

	if e.Source != "" || e.BySource != nil {
		t.Errorf("source = %q, by_source = %#v, want neither", e.Source, e.BySource)
	}
}

// A windowed row can name a network with no value inside the window: the
// entry must survive with an empty values object rather than a null one.
func TestNetworkWithNoValuesInWindowStillAppears(t *testing.T) {
	e := entryFor(t, []store.AreaAggregate{
		aggFrom("sofia", "oblast", 4, map[string]float64{"P2": 25},
			map[string]store.SourceAggregate{
				"sensor.community": {N: 3, Values: map[string]float64{"P2": 20}},
				"eea":              {N: 1},
			}),
	}, "sofia")

	eea, ok := e.BySource["eea"]
	if !ok {
		t.Fatalf("by_source dropped the valueless network: %#v", e.BySource)
	}
	if eea.N != 1 || eea.Values == nil || len(eea.Values) != 0 {
		t.Errorf("eea = {n:%d values:%#v}, want {n:1 values:{}}", eea.N, eea.Values)
	}

	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), `"values":null`) {
		t.Errorf("a null values object reached the wire: %s", raw)
	}
}

// legacyEntry is the /areas entry as it was before this change. An existing
// client decodes into exactly this and must see the same numbers.
type legacyEntry struct {
	Slug        string             `json:"slug"`
	Kind        string             `json:"kind"`
	SensorCount int                `json:"sensor_count"`
	Covered     bool               `json:"covered"`
	Values      map[string]float64 `json:"values"`
}

func TestAreaPayloadStaysReadableByAnOldClient(t *testing.T) {
	p := areaPayloadFrom(time.Now(), []store.AreaAggregate{
		aggFrom("sofia", "oblast", 4, map[string]float64{"P2": 25, "P1": 40},
			map[string]store.SourceAggregate{
				"sensor.community": {N: 3, Values: map[string]float64{"P2": 20}},
				"eea":              {N: 1, Values: map[string]float64{"P2": 100}},
			}),
	})
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var old struct {
		Areas []legacyEntry `json:"areas"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("an old client cannot decode the body: %v", err)
	}
	if len(old.Areas) != 1 {
		t.Fatalf("got %d areas, want 1", len(old.Areas))
	}
	e := old.Areas[0]
	if e.SensorCount != 4 || !e.Covered || e.Values["P2"] != 25 || e.Values["P1"] != 40 {
		t.Errorf("old view = %#v, want the unchanged 4 / true / P2 25 / P1 40", e)
	}
}
