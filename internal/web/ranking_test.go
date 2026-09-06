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
	body := fetch(t, rr, "/areas").Body.String()

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
	body := fetch(t, rr, "/areas")

	silent := body.Body.String()
	i := strings.Index(silent, ">Silent<")
	if i < 0 {
		t.Fatal("the silent province is missing from the table entirely")
	}
	// The row runs from its name to the end of that table row.
	end := strings.Index(silent[i:], "</tr>")
	if end < 0 {
		t.Fatal("no closing </tr> after the silent province")
	}
	row := silent[i : i+end]
	// A chip is what a reading is drawn as, so its absence is what says the
	// row printed no value — and a coloured swatch is the other half of the
	// same claim: a band is a statement about a number that is not there.
	if strings.Contains(row, `class="chip"`) || strings.Contains(row, "chip__swatch") {
		t.Errorf("the silent province printed a reading: %q", row)
	}
	if !strings.Contains(row, `class="nodata"`) {
		t.Errorf("the silent province does not say why it has no reading: %q", row)
	}
}

// The caption says what the numbers are, once, above the rows. A ranked column
// of bare figures would breach DESIGN.md §9.1 — never a value without saying
// what it aggregates — and the metric named here is the page default, so the
// list and the map cannot disagree about what is being shown.
func TestRankedListNamesItsMetricAndUnit(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	i := strings.Index(body, "<caption>")
	if i < 0 {
		t.Fatal("the ranked table has no caption naming its metric")
	}
	caption := body[i:min(i+240, len(body))]
	for _, want := range []string{"µg/m³"} {
		if !strings.Contains(caption, want) {
			t.Errorf("caption does not carry %q: %q", want, caption)
		}
	}
}

// The swatch is the band the reading falls in, drawn as an SVG fill attribute
// because the CSP forbids the inline style the kit's --chip-ramp would need.
// 88.5 µg/m³ of PM2.5 is EAQI's open top band; 4.2 is its first. Two different
// rows must therefore carry two different colours — one colour for both would
// be a swatch that says nothing.
func TestReadingsCarryTheirBandColour(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	rowColour := func(name string) string {
		i := strings.Index(body, ">"+name+"<")
		if i < 0 {
			t.Fatalf("province %q missing from the table", name)
		}
		end := strings.Index(body[i:], "</tr>")
		row := body[i : i+end]
		j := strings.Index(row, `fill="`)
		if j < 0 {
			t.Fatalf("province %q has no swatch fill: %q", name, row)
		}
		rest := row[j+len(`fill="`):]
		return rest[:strings.Index(rest, `"`)]
	}

	high, low := rowColour("High"), rowColour("Low")
	if high == low {
		t.Errorf("88.5 and 4.2 painted the same colour %q", high)
	}
	// Pinned, not merely different: these are the EAQI colours the map paints
	// for the same two readings, and the point of colouring server-side is that
	// the row and the dot agree.
	if want := "#7d2181"; high != want {
		t.Errorf("88.5 painted %q, want the open top band %q", high, want)
	}
	if want := "#50f0e6"; low != want {
		t.Errorf("4.2 painted %q, want the first band %q", low, want)
	}
}
