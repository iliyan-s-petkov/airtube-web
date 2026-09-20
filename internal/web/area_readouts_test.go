package web

// In package web rather than web_test for the same reason readouts_test.go is:
// PageData.cat is unexported and AreaReadouts reads the catalogue.

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

// areaReadoutsFor builds the PageData an area page carries — the one area, the
// canonical metric order, and the catalogue — and asks for its strip.
func areaReadoutsFor(t *testing.T, lang string, area AreaRow) []Readout {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	p := PageData{
		Lang:          lang,
		Area:          &area,
		DefaultMetric: "P2",
		// The order the site publishes metrics in, which the strip must follow —
		// a map's iteration order would reshuffle the cells on every request.
		Metrics: []string{"P2", "P1", "temperature", "humidity", "pressure", "noise_LAeq", "noise_LA_max"},
		cat:     cat,
	}
	return p.AreaReadouts()
}

// area is the shorthand these cases are written in.
func area(kind string, sensors int, values map[string]float64) AreaRow {
	return AreaRow{Name: "Пловдив", Kind: kind, Covered: true, SensorCount: sensors, Values: values}
}

func TestAreaReadoutsGiveOneCellPerMeasuredMetric(t *testing.T) {
	got := areaReadoutsFor(t, "bg", area("oblast", 111, map[string]float64{
		"P2": 3.22, "P1": 7.7, "temperature": 14.0,
	}))
	// Three metrics measured plus the sensor count.
	if len(got) != 4 {
		t.Fatalf("got %d cells, want 4: three measured metrics and the sensor count", len(got))
	}
	if got[0].Value != "3,2" || got[0].Unit != "µg/m³" {
		t.Errorf("first cell = %q %q, want the default metric 3,2 µg/m³", got[0].Value, got[0].Unit)
	}
	if got[2].Unit != "°C" {
		t.Errorf("temperature unit = %q, want °C — each metric carries its own", got[2].Unit)
	}
	// No unit on the count: its label already says what was counted.
	if got[3].Value != "111" || got[3].Unit != "" {
		t.Errorf("sensor cell = %q %q, want a bare 111", got[3].Value, got[3].Unit)
	}
}

// The cells follow the site's canonical metric order, not the order the values
// map happens to iterate in — otherwise the strip would rearrange itself
// between two requests for the same page.
func TestAreaReadoutsKeepTheCanonicalMetricOrder(t *testing.T) {
	got := areaReadoutsFor(t, "en", area("oblast", 4, map[string]float64{
		"humidity": 60, "P2": 3.2, "temperature": 14,
	}))
	want := []string{"PM2.5", "Temperature", "Humidity"}
	for i, label := range want {
		if got[i].Label != label {
			t.Errorf("cell %d = %q, want %q", i, got[i].Label, label)
		}
	}
}

// The default metric leads whatever the canonical order says. Canonical order
// is alphabetical, so P1 sorts above P2 — and a strip opening on PM10 while
// the chart, the map and the province list all show PM2.5 answers a question
// the page is not asking.
func TestAreaReadoutsOpenOnThePageDefaultMetric(t *testing.T) {
	got := areaReadoutsFor(t, "en", area("oblast", 4, map[string]float64{
		"P1": 7.7, "P2": 3.2, "temperature": 14,
	}))
	want := []string{"PM2.5", "PM10", "Temperature"}
	for i, label := range want {
		if got[i].Label != label {
			t.Errorf("cell %d = %q, want %q", i, got[i].Label, label)
		}
	}
}

// And it appears exactly once — leading it without excluding it from the loop
// prints the site's headline metric twice.
func TestAreaReadoutsDoNotRepeatTheDefaultMetric(t *testing.T) {
	got := areaReadoutsFor(t, "en", area("oblast", 4, map[string]float64{"P2": 3.2, "P1": 7.7}))
	if len(got) != 3 {
		t.Fatalf("got %d cells, want 3: two metrics and the sensor count", len(got))
	}
}

// A metric the area is not measuring gets no cell at all. An empty cell would
// be a claim that the site tried and found nothing, when in fact this area has
// no sensor carrying that instrument.
func TestAreaReadoutsSkipMetricsWithNoReading(t *testing.T) {
	got := areaReadoutsFor(t, "en", area("oblast", 4, map[string]float64{"P2": 3.2}))
	if len(got) != 2 {
		t.Fatalf("got %d cells, want 2: one measured metric and the sensor count", len(got))
	}
}

// 0 µg/m³ is a reading. Treating a zero as "not measured" would silently drop
// the cell on the cleanest night of the year.
func TestAreaReadoutsTreatZeroAsAReading(t *testing.T) {
	got := areaReadoutsFor(t, "bg", area("oblast", 4, map[string]float64{"P2": 0}))
	if len(got) != 2 || got[0].Value != "0,0" {
		t.Fatalf("got %d cells, first %q — want a cell reading 0,0", len(got), got[0].Value)
	}
}

// The tier line is what stops a bare number claiming to be a place. A province
// page reports a province; a city page reports a city, and saying "province
// median" on Пловдив-град would be false.
func TestAreaReadoutsNameTheTierTheySummarise(t *testing.T) {
	oblast := areaReadoutsFor(t, "en", area("oblast", 4, map[string]float64{"P2": 3.2}))
	city := areaReadoutsFor(t, "en", area("city", 4, map[string]float64{"P2": 3.2}))
	if oblast[0].Tier != "province median" {
		t.Errorf("oblast tier = %q, want the province wording", oblast[0].Tier)
	}
	if city[0].Tier != "city median" {
		t.Errorf("city tier = %q, want the city wording", city[0].Tier)
	}
	if oblast[1].Tier == "" || city[1].Tier == "" {
		t.Error("the sensor cell has no tier line: a bare count does not say what it counts")
	}
	// The home page's "Sensors in the network" is the country total. Borrowing
	// that label here would put a national claim over one province's count.
	if oblast[1].Label != "Sensors" {
		t.Errorf("sensor label = %q, want the plain column label, not the network-wide one", oblast[1].Label)
	}
}

// An area below the coverage floor publishes no average, so it gets no strip —
// the page already states the absence in its own words. Four cells of nothing
// would contradict that notice.
func TestAreaReadoutsAreAbsentWithoutCoverage(t *testing.T) {
	a := area("oblast", 2, map[string]float64{"P2": 3.2})
	a.Covered = false
	if got := areaReadoutsFor(t, "bg", a); got != nil {
		t.Errorf("AreaReadouts() = %v, want nil for an uncovered area", got)
	}
}

// A covered area with every sensor silent still counts its sensors: that is a
// fact about the network, and it is the number that explains the silence.
func TestAreaReadoutsStillCountSensorsWithNoReadings(t *testing.T) {
	got := areaReadoutsFor(t, "bg", area("oblast", 9, nil))
	if len(got) != 1 || got[0].Value != "9" {
		t.Fatalf("got %v, want a single cell counting 9 sensors", got)
	}
}

// The strip only exists on a page that has an area. Home and about carry none.
func TestAreaReadoutsAreAbsentWithoutAnArea(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	p := PageData{Lang: "bg", cat: cat}
	if got := p.AreaReadouts(); got != nil {
		t.Errorf("AreaReadouts() = %v, want nil with no area", got)
	}
}

// The same rule the province rows and the country strip follow: the decimal
// separator belongs to the language.
func TestAreaReadoutsWriteTheDecimalTheLanguageWrites(t *testing.T) {
	values := map[string]float64{"P2": 18.16}
	if got := areaReadoutsFor(t, "en", area("oblast", 4, values)); got[0].Value != "18.2" {
		t.Errorf("English = %q, want 18.2", got[0].Value)
	}
	if got := areaReadoutsFor(t, "bg", area("oblast", 4, values)); got[0].Value != "18,2" {
		t.Errorf("Bulgarian = %q, want 18,2", got[0].Value)
	}
}

// Every cell is labelled, valued and tiered in both languages. An empty label
// is what an invented catalogue key looks like — the startup key check cannot
// see a key this file made up and never added.
func TestEveryAreaReadoutIsLabelledAndTiered(t *testing.T) {
	for _, lang := range []string{"bg", "en"} {
		got := areaReadoutsFor(t, lang, area("oblast", 4, map[string]float64{"P2": 3.2, "noise_LAeq": 51}))
		if len(got) != 3 {
			t.Fatalf("%s: got %d cells, want 3", lang, len(got))
		}
		for i, r := range got {
			if r.Label == "" || r.Tier == "" || r.Value == "" {
				t.Errorf("%s: cell %d is incomplete: %+v", lang, i, r)
			}
		}
	}
}

// mixed is a covered area fed by both networks.
func mixed(kind string, sensors int, values map[string]float64) AreaRow {
	a := area(kind, sensors, values)
	a.BySource = map[string]snapshot.SourceEntry{
		"sensor.community": {N: 3, Values: map[string]float64{"P2": 20}},
		"eea":              {N: 1, Values: map[string]float64{"P2": 100}},
	}
	return a
}

// groupsOf collects the cells that belong to a network heading.
func groupsOf(rs []Readout) []Readout {
	var out []Readout
	for _, r := range rs {
		if r.Group != "" {
			out = append(out, r)
		}
	}
	return out
}

func TestAreaReadoutsAddARowPerNetwork(t *testing.T) {
	got := areaReadoutsFor(t, "en", mixed("oblast", 4, map[string]float64{"P2": 25}))

	// The main row is untouched: one metric cell plus the station count.
	if got[0].Value != "25.0" || got[0].Group != "" {
		t.Errorf("first cell = %q group %q, want the blended 25.0 with no group", got[0].Value, got[0].Group)
	}
	if got[1].Label != "Sensors" || got[1].Value != "4" || got[1].Group != "" {
		t.Errorf("count cell = %q %q group %q, want Sensors 4 ungrouped", got[1].Label, got[1].Value, got[1].Group)
	}

	gs := groupsOf(got)
	if len(gs) != 2 {
		t.Fatalf("got %d grouped cells, want 2 — one per network", len(gs))
	}
	// sort.Strings puts eea first, on every run.
	if gs[0].Group != "Official" || gs[0].Value != "100.0" {
		t.Errorf("first group = %q %q, want Official 100.0", gs[0].Group, gs[0].Value)
	}
	if gs[0].Tier != "1 station" {
		t.Errorf("first group tier = %q, want \"1 station\"", gs[0].Tier)
	}
	if gs[1].Group != "Citizen" || gs[1].Value != "20.0" {
		t.Errorf("second group = %q %q, want Citizen 20.0", gs[1].Group, gs[1].Value)
	}
	if gs[1].Tier != "3 stations" {
		t.Errorf("second group tier = %q, want \"3 stations\"", gs[1].Tier)
	}
	if strings.Contains(gs[0].Tier, "Official") || strings.Contains(gs[0].Tier, "·") {
		t.Errorf("tier %q still carries the network name or its separator", gs[0].Tier)
	}
}

// The group order is a contract, not a coincidence: two requests for the same
// page must produce a byte-identical strip.
func TestAreaReadoutsOrderNetworkGroupsBySortedKey(t *testing.T) {
	gs := groupsOf(areaReadoutsFor(t, "en", mixed("oblast", 4, map[string]float64{"P2": 25})))
	if len(gs) != 2 {
		t.Fatalf("got %d grouped cells, want 2", len(gs))
	}
	if gs[0].Group != "Official" || gs[1].Group != "Citizen" {
		t.Errorf("group order = %q, %q — eea sorts before sensor.community, so Official leads",
			gs[0].Group, gs[1].Group)
	}
}

// One network feeding the area is not a breakdown: the strip already IS that
// network's numbers.
func TestAreaReadoutsAddNoRowForASingleNetwork(t *testing.T) {
	a := area("oblast", 4, map[string]float64{"P2": 25})
	a.Source = "sensor.community"
	got := areaReadoutsFor(t, "en", a)

	if gs := groupsOf(got); len(gs) != 0 {
		t.Errorf("got %d grouped cells, want none for a one-network area", len(gs))
	}
	if len(got) != 2 {
		t.Errorf("got %d cells, want the unchanged 2", len(got))
	}
}

// The Bulgarian page says it in Bulgarian, from bg.json.
func TestAreaReadoutsNameNetworksInBulgarian(t *testing.T) {
	gs := groupsOf(areaReadoutsFor(t, "bg", mixed("oblast", 4, map[string]float64{"P2": 25})))
	if len(gs) != 2 {
		t.Fatalf("got %d grouped cells, want 2", len(gs))
	}
	if gs[0].Group != "Официални" || gs[1].Group != "Граждански" {
		t.Errorf("groups = %q, %q — want Официални then Граждански", gs[0].Group, gs[1].Group)
	}
	if gs[0].Tier != "1 станция" {
		t.Errorf("tier = %q, want \"1 станция\"", gs[0].Tier)
	}
}

func TestAreaReadoutsBreakdownIsAbsentWithoutCoverage(t *testing.T) {
	a := mixed("oblast", 4, map[string]float64{"P2": 25})
	a.Covered = false
	if got := areaReadoutsFor(t, "bg", a); got != nil {
		t.Errorf("AreaReadouts() = %v, want nil for an uncovered area", got)
	}
}

// A network reporting nothing for a metric gets no cell for it.
func TestAreaReadoutsSkipMetricsANetworkDoesNotReport(t *testing.T) {
	a := area("oblast", 4, map[string]float64{"P2": 25, "humidity": 55})
	a.BySource = map[string]snapshot.SourceEntry{
		"sensor.community": {N: 3, Values: map[string]float64{"P2": 20, "humidity": 55}},
		"eea":              {N: 1, Values: map[string]float64{"P2": 100}},
	}
	gs := groupsOf(areaReadoutsFor(t, "en", a))
	// eea: one cell. sensor.community: two.
	if len(gs) != 3 {
		t.Fatalf("got %d grouped cells, want 3", len(gs))
	}
	for _, r := range gs {
		// unit.humidity is "%" in en.json.
		if r.Group == "Official" && r.Unit == "%" {
			t.Error("the official row carries a humidity cell it has no reading for")
		}
	}
}
