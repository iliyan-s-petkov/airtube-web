package web_test

import (
	"strings"
	"testing"
)

// The finder reads the rendered rows rather than carrying a second copy of the
// 28 names (see web/src/islands/finder.js). That only works while the selector
// the island is given still selects the table the page renders — and the list
// this pair pointed at was a <ul class="areas"> until the table replaced it, so
// this is a pair that has already been broken once by a markup change.
func TestFinderReadsTheRenderedTable(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	const source = `data-source=".table tbody"`
	if !strings.Contains(body, source) {
		t.Fatalf("the finder island does not carry %s", source)
	}
	if !strings.Contains(body, `<table class="table">`) || !strings.Contains(body, "<tbody>") {
		t.Error("the finder's selector names a table and a tbody the page does not render")
	}
	// The name is an anchor, which is what readAreas lifts. A row whose name
	// stopped being a link would leave the finder with fewer areas than the
	// page shows, silently.
	if !strings.Contains(body, `<td class="name"><a class="link" href=`) {
		t.Error("the province name is not a link, so the finder can no longer read it")
	}
}

// The line under the table says how many rows it is showing and how many of
// them are silent. Both are counted from the rows themselves — a stored total
// is how that sentence starts disagreeing with the table above it.
func TestCountLineMatchesTheRows(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, `class="meta"`)
	if i < 0 {
		t.Fatal("the table has no count line")
	}
	line := body[i:min(i+200, len(body))]
	// Five provinces in the fixture, one of them ("Silent") with no reading.
	if !strings.Contains(line, "5 ") {
		t.Errorf("the count line does not name the five rendered rows: %q", line)
	}
	if !strings.Contains(line, "1 ") {
		t.Errorf("the count line does not name the one silent row: %q", line)
	}
}

// A silent province still prints its sensor count: the absence is of a reading,
// not of the province, and hiding the number would make an ordinary state of
// the network look like a province that does not exist.
func TestSilentProvinceStillPrintsItsSensorCount(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, ">Silent<")
	row := body[i : i+strings.Index(body[i:], "</tr>")]
	if !strings.Contains(row, `<td class="sensors">3</td>`) {
		t.Errorf("the silent province hid its sensor count: %q", row)
	}
}

// The column header names the metric and its unit, so the figures under it are
// never bare (DESIGN.md §9.1) — and it names the PAGE's default metric, which
// is the one the map paints and the one the rows are ranked by.
func TestValueColumnHeaderNamesTheMetric(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, `<th scope="col" class="num">`)
	if i < 0 {
		t.Fatal("the value column has no header")
	}
	head := body[i:min(i+120, len(body))]
	if !strings.Contains(head, "µg/m³") {
		t.Errorf("the value column header carries no unit: %q", head)
	}
}

// Two different absences, said differently. A province with too few reporting
// sensors to publish an average at all is not the same fact as a covered
// province with no recent reading for this metric, and one sentence for both
// would tell a reader to wait for data that is never coming.
func TestTheTwoAbsencesReadDifferently(t *testing.T) {
	snap := rankingSnapshot()
	meta := snap.KnownSlugs["silent"]
	meta.Covered = false
	snap.KnownSlugs["silent"] = meta

	rr := renderer(t, snap)
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, ">Silent<")
	row := body[i : i+strings.Index(body[i:], "</tr>")]
	// "/" is the Bulgarian page, so these are the bg.json wordings of
	// area.no_coverage and table.no_recent respectively.
	if !strings.Contains(row, "Недостатъчно данни") {
		t.Errorf("an uncovered province does not say so: %q", row)
	}
	if strings.Contains(row, "Няма скорошни данни") {
		t.Errorf("an uncovered province claims it merely has no recent reading: %q", row)
	}
}
