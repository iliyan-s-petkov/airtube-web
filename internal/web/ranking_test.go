package web_test

import (
	"strings"
	"testing"
	"time"

	"airbg.org/internal/snapshot"
)

// The province list is ranked by its reading, and the order is a contract, not
// a convenience: a reader opens the home page to find out where the air is bad.
// Nothing asserted it, so a resort or a flipped tier would have shipped green.
//
// The fixture is built to make each rule fail independently if it breaks:
//   - "high" and "low" pin the descending order.
//   - "tie-b" and "tie-a" share a value, so only the name tiebreak can order
//     them — and they are inserted in the wrong order on purpose.
//   - "silent" has no reading at all. Its value would sort FIRST if absence
//     were ever encoded as a zero, so this row is what catches that mistake.
func rankingSnapshot() *snapshot.Snapshot {
	meta := func(slug, name string, values map[string]float64) snapshot.AreaMeta {
		return snapshot.AreaMeta{
			Slug: slug, Kind: "oblast", NameBG: name, NameEN: name,
			Covered: true, SensorCount: 3, Values: values,
		}
	}
	return &snapshot.Snapshot{
		GeneratedAt: time.Now(),
		KnownSlugs: map[string]snapshot.AreaMeta{
			"low":    meta("low", "Low", map[string]float64{"P2": 4.2}),
			"tie-b":  meta("tie-b", "Bravo", map[string]float64{"P2": 9}),
			"silent": meta("silent", "Silent", nil),
			"high":   meta("high", "High", map[string]float64{"P2": 88.5}),
			"tie-a":  meta("tie-a", "Alpha", map[string]float64{"P2": 9}),
		},
	}
}

func TestProvinceListIsRankedByReading(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	want := []string{"High", "Alpha", "Bravo", "Low", "Silent"}
	at := make([]int, len(want))
	for i, name := range want {
		at[i] = strings.Index(body, ">"+name+"<")
		if at[i] < 0 {
			t.Fatalf("province %q missing from the rendered list", name)
		}
	}
	for i := 1; i < len(want); i++ {
		if at[i] < at[i-1] {
			t.Errorf("%q renders before %q; want order %v", want[i], want[i-1], want)
		}
	}
}

// An area with no reading must not print one. 0 is a legitimate value, so if
// absence were ever encoded as a float zero this is where it would surface —
// as a confident "0,0" for a province that measured nothing, which is the
// fabrication this project has removed twice before.
func TestSilentProvincePrintsNoReading(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/")

	silent := body.Body.String()
	i := strings.Index(silent, ">Silent<")
	if i < 0 {
		t.Fatal("the silent province is missing from the list entirely")
	}
	// The row runs from its name to the end of that list item.
	end := strings.Index(silent[i:], "</li>")
	if end < 0 {
		t.Fatal("no closing </li> after the silent province")
	}
	row := silent[i : i+end]
	if strings.Contains(row, `class="reading"`) {
		t.Errorf("the silent province printed a reading: %q", row)
	}
}

// The caption says what the numbers are, once, above the rows. A ranked column
// of bare figures would breach DESIGN.md §9.1 — never a value without saying
// what it aggregates — and the metric named here is the page default, so the
// list and the map cannot disagree about what is being shown.
func TestRankedListNamesItsMetricAndUnit(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, `class="areas-caption"`)
	if i < 0 {
		t.Fatal("the ranked list has no caption naming its metric")
	}
	caption := body[i:min(i+240, len(body))]
	for _, want := range []string{"µg/m³"} {
		if !strings.Contains(caption, want) {
			t.Errorf("caption does not carry %q: %q", want, caption)
		}
	}
}
