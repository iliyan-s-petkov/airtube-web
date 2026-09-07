package web_test

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// "/" and "/areas" render from one template. They used to render the SAME
// thing — map, then the full province table under it — so the two tabs in the
// masthead led to near-identical pages and every visit to the home page
// shipped all 28 provinces twice. Each tab now shows its own subject.

func TestMapTabDoesNotRepeatTheProvinceTable(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	if strings.Contains(body, `<table class="table">`) {
		t.Error(`the map tab still renders the province table; that is the /areas tab's page`)
	}
	if strings.Contains(body, `data-island="table"`) {
		t.Error("the map tab mounts the table island with no table to act on")
	}
	if !strings.Contains(body, `data-island="map"`) {
		t.Fatal("the map tab does not render the map")
	}
}

// Removing the table from the map tab removed that tab's text alternative with
// it. The map still declares aria-describedby="map-alt", so the element has to
// exist — and it has to lead somewhere, because it is now the only route from
// the map to the numbers for a reader who cannot use it, and the only link a
// crawler can follow off the home page to the readings.
func TestMapTabPointsAtTheAreasTabForTheNumbers(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	if !strings.Contains(body, `aria-describedby="map-alt"`) {
		t.Fatal("the map no longer names its description")
	}
	i := strings.Index(body, `id="map-alt"`)
	if i < 0 {
		t.Fatal(`aria-describedby names #map-alt, which the page does not render`)
	}
	if !strings.Contains(body[i:min(i+400, len(body))], `href="/areas"`) {
		t.Error("the map's description does not link to the list it describes")
	}
}

func TestAreasTabIsTheListAndNotASecondMap(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/areas").Body.String()

	if !strings.Contains(body, `<table class="table">`) {
		t.Fatal("the areas tab does not render the province table")
	}
	if strings.Contains(body, `data-island="map"`) {
		t.Error("the areas tab renders a second map; the map tab is where the map lives")
	}
	// The finder searches the rendered rows here, and says so: data-source is
	// what tells the island to read the table rather than the map.
	if !strings.Contains(body, `data-island="finder"`) {
		t.Error("the areas tab does not mount the finder over its own list")
	}
	if !strings.Contains(body, `data-source=".table tbody"`) {
		t.Error("the areas tab's finder does not name the list it searches")
	}
}

// The finder is on the map tab too, over the areas the map has loaded. Its
// absent data-source is the whole switch (web/src/islands/finder.js), so an
// attribute copied onto this tab would send the field looking for a table that
// is not there — and the hint has to say what Enter does HERE, which is centre
// the map, not open a page.
func hintMap(t *testing.T) string {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	return cat.T("bg", "find.hint_map")
}

func TestMapTabFindsAProvinceOnTheMap(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	i := strings.Index(body, `data-island="finder"`)
	if i < 0 {
		t.Fatal("the map tab mounts no finder")
	}
	island := body[i:min(i+600, len(body))]
	if strings.Contains(island, "data-source=") {
		t.Error("the map tab's finder names a source list; there is no table on this tab to read")
	}
	if !strings.Contains(island, hintMap(t)) {
		t.Error("the map tab's finder promises the list tab's Enter, which opens a page instead of moving the map")
	}
}
