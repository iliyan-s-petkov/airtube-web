package web

// In package web rather than web_test because PageData.cat is unexported and
// Readouts reads the catalogue: a PageData built from outside has no
// translations and every Label would come back empty, which would make these
// tests agree with a broken implementation.

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// readoutsFor builds a PageData carrying rows and nothing else the strip reads.
// The rows are given in the order the page renders them — ranked by reading —
// because one of the assertions below is that computing the strip does not
// disturb that order.
func readoutsFor(t *testing.T, lang string, rows []AreaRow) []Readout {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	p := PageData{Lang: lang, Areas: rows, DefaultMetric: "P2", cat: cat}
	return p.Readouts()
}

// row is the shorthand the cases below are written in: name, reading, whether
// there is one, and the sensor count the province reports.
func row(name string, value float64, has bool, covered bool, sensors int) AreaRow {
	return AreaRow{Name: name, Value: value, HasValue: has, Covered: covered, SensorCount: sensors}
}

func TestReadoutsNamesTheHighestProvince(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("Смолян", 18.16, true, true, 4),
		row("Пловдив", 9.0, true, true, 30),
		row("Видин", 3.1, true, true, 2),
	})
	if got[0].Value != "18,2" {
		t.Errorf("highest = %q, want the largest reading 18,2", got[0].Value)
	}
	if !strings.HasPrefix(got[0].Tier, "Смолян ") {
		t.Errorf("highest tier = %q, want it to name Смолян", got[0].Tier)
	}
	if got[0].Unit != "µg/m³" {
		t.Errorf("highest unit = %q, want the default metric's unit", got[0].Unit)
	}
}

// The largest reading arriving LAST is the case a >= comparison and a
// first-wins loop both get wrong in one direction or the other.
func TestReadoutsFindsTheHighestWhereverItSits(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("Видин", 3.1, true, true, 2),
		row("Смолян", 18.2, true, true, 4),
	})
	if !strings.HasPrefix(got[0].Tier, "Смолян ") {
		t.Errorf("highest tier = %q, want Смолян even when it is not first", got[0].Tier)
	}
}

// A province reading 0.0 is a measurement, not an absence — so it must be
// eligible to be the highest when it is the only one. Initialising the running
// maximum to zero rather than to the first reading loses exactly this.
func TestReadoutsTreatsZeroAsAReading(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{row("Видин", 0, true, true, 1)})
	if !strings.HasPrefix(got[0].Tier, "Видин ") {
		t.Errorf("highest tier = %q, want Видин: 0 is a reading", got[0].Tier)
	}
	if got[0].Value != "0,0" {
		t.Errorf("highest = %q, want 0,0", got[0].Value)
	}
}

// Seven metrics ship, and temperature goes below zero every winter. A running
// maximum seeded at 0 rather than at the first reading names no province at
// all on a night when every one of them is under freezing.
func TestReadoutsHandlesReadingsBelowZero(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("Смолян", -12.4, true, true, 4),
		row("Видин", -3.5, true, true, 2),
	})
	if !strings.HasPrefix(got[0].Tier, "Видин ") {
		t.Errorf("highest tier = %q, want Видин: -3,5 is the highest of the two", got[0].Tier)
	}
	if got[0].Value != "-3,5" {
		t.Errorf("highest = %q, want -3,5", got[0].Value)
	}
}

func TestReadoutsTakesTheMiddleReading(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("a", 30, true, true, 1),
		row("b", 4, true, true, 1),
		row("c", 2, true, true, 1),
	})
	// 4, not (30+4+2)/3 = 12: the median is what keeps one province in an
	// inversion from moving the national figure somewhere nobody is.
	if got[1].Value != "4,0" {
		t.Errorf("median = %q, want the middle reading 4,0", got[1].Value)
	}
}

func TestReadoutsAveragesTheTwoMiddleReadings(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("a", 10, true, true, 1),
		row("b", 5, true, true, 1),
		row("c", 3, true, true, 1),
		row("d", 2, true, true, 1),
	})
	if got[1].Value != "4,0" {
		t.Errorf("median = %q, want (5+3)/2 = 4,0", got[1].Value)
	}
}

// The median must not depend on the order the rows arrive in. Today they
// arrive ranked, and a ranked list is symmetric enough that dropping the sort
// still lands on the right value — so the case is written scrambled, where it
// does not. What the page ranks by is the page's business, not the strip's.
func TestReadoutsSortBeforeTakingTheMiddle(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("a", 2, true, true, 1),
		row("b", 30, true, true, 1),
		row("c", 4, true, true, 1),
	})
	if got[1].Value != "4,0" {
		t.Errorf("median = %q, want 4,0 — the readings were not in order", got[1].Value)
	}
}

// The strip is computed from the list the page renders below it, so computing
// it must not reorder that list — the rows arrive ranked by reading and the
// median needs them sorted.
func TestReadoutsLeavesTheProvinceOrderAlone(t *testing.T) {
	rows := []AreaRow{
		row("a", 30, true, true, 1),
		row("b", 4, true, true, 1),
		row("c", 2, true, true, 1),
	}
	readoutsFor(t, "bg", rows)
	for i, want := range []string{"a", "b", "c"} {
		if rows[i].Name != want {
			t.Fatalf("row %d = %q, want %q: the page's ranking was disturbed", i, rows[i].Name, want)
		}
	}
}

// Only covered provinces feed the aggregates the map draws, so only their
// sensors are in the network figure — otherwise the strip's total would
// disagree with what the map is built from.
func TestReadoutsCountsSensorsInCoveredProvincesOnly(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("a", 5, true, true, 30),
		row("b", 0, false, false, 7),
		row("c", 3, true, true, 12),
	})
	if got[2].Value != "42" {
		t.Errorf("sensors = %q, want 42 — the uncovered province's 7 do not count", got[2].Value)
	}
	if got[3].Value != "1" {
		t.Errorf("no-data = %q, want 1", got[3].Value)
	}
	if !strings.HasPrefix(got[1].Tier, "2 ") {
		t.Errorf("median tier = %q, want it to open with the count of provinces with data", got[1].Tier)
	}
}

// Every province silent is an ordinary night on a small network, not an error:
// the two measured cells say there is no reading, and the two counted cells
// still count.
func TestReadoutsSaysSoWhenNothingIsReporting(t *testing.T) {
	got := readoutsFor(t, "bg", []AreaRow{
		row("a", 0, false, true, 3),
		row("b", 0, false, true, 2),
	})
	none := "няма измерване"
	if got[0].Value != none || got[1].Value != none {
		t.Errorf("highest/median = %q/%q, want %q for both", got[0].Value, got[1].Value, none)
	}
	if got[0].Unit != "" || got[1].Unit != "" {
		t.Errorf("units = %q/%q, want none: there is no quantity to carry one", got[0].Unit, got[1].Unit)
	}
	if got[2].Value != "5" || got[3].Value != "2" {
		t.Errorf("sensors/no-data = %q/%q, want 5/2", got[2].Value, got[3].Value)
	}
	// Including the tier line, which is where "why is there no figure" lives.
	// A blank third line here reads as a cell that failed to render.
	for i, r := range got {
		if r.Tier == "" {
			t.Errorf("readout %d (%s) has no tier line with nothing reporting", i, r.Label)
		}
	}
}

// A page with no province list at all — about, error — renders no strip rather
// than four zeroes, which would be four claims about the country that nothing
// on the page supports.
func TestReadoutsAreAbsentWithoutAProvinceList(t *testing.T) {
	if got := readoutsFor(t, "bg", nil); got != nil {
		t.Errorf("Readouts() = %v, want nil with no areas", got)
	}
}

// The decimal separator is the language's, and it is the same rule the rows
// below use — see formatValue. A strip printing 18.2 above a list printing
// 18,2 reads as two different measurements.
func TestReadoutsWriteTheDecimalTheLanguageWrites(t *testing.T) {
	rows := []AreaRow{row("Smolyan", 18.16, true, true, 4)}
	if got := readoutsFor(t, "en", rows); got[0].Value != "18.2" {
		t.Errorf("English highest = %q, want 18.2", got[0].Value)
	}
	if got := readoutsFor(t, "bg", rows); got[0].Value != "18,2" {
		t.Errorf("Bulgarian highest = %q, want 18,2", got[0].Value)
	}
}

// Every cell carries a translated label and a tier line. A cell that is a bare
// number does not say whether it is one sensor, one province or the country
// (DESIGN.md §9.1), and an empty label is what a missing catalogue key looks
// like — the startup check cannot see a key this file invented and never added.
func TestEveryReadoutIsLabelledAndTiered(t *testing.T) {
	for _, lang := range []string{"bg", "en"} {
		got := readoutsFor(t, lang, []AreaRow{row("a", 5, true, true, 3)})
		if len(got) != 4 {
			t.Fatalf("%s: got %d readouts, want 4", lang, len(got))
		}
		for i, r := range got {
			if r.Label == "" || r.Tier == "" || r.Value == "" {
				t.Errorf("%s: readout %d is incomplete: %+v", lang, i, r)
			}
		}
	}
}
