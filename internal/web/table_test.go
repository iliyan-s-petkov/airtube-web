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
	body := fetch(t, rr, "/areas").Body.String()

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
	body := fetch(t, rr, "/areas").Body.String()

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
	body := fetch(t, rr, "/areas").Body.String()

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
	body := fetch(t, rr, "/areas").Body.String()

	i := strings.Index(body, `<th scope="col" class="num"`)
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
	body := fetch(t, rr, "/areas").Body.String()

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

// The island sorts on these, not on the printed cells: the reading is written
// 12,4 in Bulgarian and 12.4 in English, and a sort that parsed the text would
// order the table differently in the two languages.
func TestRowsCarryTheirSortKeys(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	i := strings.Index(body, ">Silent<")
	if i < 0 {
		t.Fatal("the silent province is missing")
	}
	// Back up to the row's opening tag.
	open := strings.LastIndex(body[:i], "<tr ")
	if open < 0 {
		t.Fatal("the silent province's row carries no attributes at all")
	}
	row := body[open : open+strings.Index(body[open:], ">")]
	if !strings.Contains(row, "data-nodata") {
		t.Errorf("a silent row is not marked as one, so it would sort as a small reading: %q", row)
	}
	if strings.Contains(row, "data-value") {
		t.Errorf("a silent row carries a value: %q", row)
	}
	if !strings.Contains(row, `data-sensors="3"`) {
		t.Errorf("the row carries no sensor count to sort on: %q", row)
	}
	// A row WITH a reading carries the unrounded figure, at more precision than
	// the cell prints: the sort follows the ranking, not the rounding.
	if !strings.Contains(body, `data-value="88.5000"`) {
		t.Error("a reading row carries no sortable value")
	}
}

// Three columns, three keys. A header the island cannot name is a column the
// reader cannot sort by, silently.
func TestEveryColumnIsSortable(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	for _, key := range []string{`data-sort-key="name"`, `data-sort-key="value"`, `data-sort-key="sensors"`} {
		if !strings.Contains(body, key) {
			t.Errorf("no column carries %s", key)
		}
	}
	// The server's own order, stated on the column it ordered by — true with or
	// without JavaScript, and the island keeps it in step from there.
	if !strings.Contains(body, `data-sort-key="value" aria-sort="descending"`) {
		t.Error("the ranked column does not say it is the sorted one")
	}
	if strings.Count(body, "aria-sort=") != 1 {
		t.Error("more than one column claims to be the sorted one")
	}
}

// The controls are not in the served HTML: they are interactive by nature, and
// a header or a pager that does nothing without JavaScript is worse than one
// that never claimed to be a control. What ships is the mount point.
func TestTheControlsAreNotServerRendered(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	if !strings.Contains(body, `data-island="table"`) {
		t.Fatal("the table island has no mount point")
	}
	for _, dead := range []string{`class="th-sort"`, `class="pager"`, `<select`} {
		if strings.Contains(body, dead) {
			t.Errorf("the server rendered %s, which does nothing without JavaScript", dead)
		}
	}
	// The empty-state line is the exception, and it ships hidden: the island has
	// only to unhide it, so the sentence is the catalogue's rather than a
	// string built in JavaScript.
	if !strings.Contains(body, `<p class="t-empty" hidden>`) {
		t.Error("the empty-filter sentence is missing or not hidden")
	}
}
