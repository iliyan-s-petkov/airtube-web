package web_test

import (
	"strings"
	"testing"
)

// The period switcher is offered from the server's own vocabulary rather than
// from a list written into a template or an island. The API rejects any period
// it does not know (see api.parsePeriod), so a hard-coded list is a button that
// returns 400 the day someone edits the config.
func TestPeriodsComeFromTheConfiguredVocabulary(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/area/silent").Body.String()

	if !strings.Contains(body, `data-periods="24h,7d,30d,1y"`) {
		t.Error("the chart island is not given the configured period vocabulary in file order")
	}
	// File order, not alphabetical: 1y sorts first alphabetically, and a
	// switcher opening on "1 year" would put the coarsest view where the
	// default belongs.
	if !strings.Contains(body, `data-period="24h"`) {
		t.Error("the chart island is not given the default period")
	}
}

// Labels ride alongside as a positional list, the same shape the metric
// switcher already uses: the island reads them by index, so an island that had
// to translate would need the catalogue shipped to the browser.
func TestPeriodLabelsAreTranslatedServerSide(t *testing.T) {
	rr := renderer(t, rankingSnapshot())

	bg := fetch(t, rr, "/area/silent").Body.String()
	if !strings.Contains(bg, `data-period-labels="24 часа,7 дни,30 дни,1 година"`) {
		t.Error("the Bulgarian page does not carry the Bulgarian period labels")
	}

	en := fetch(t, rr, "/en/area/silent").Body.String()
	if !strings.Contains(en, `data-period-labels="24 hours,7 days,30 days,1 year"`) {
		t.Error("the English page does not carry the English period labels")
	}
}

// The chart's heading is composed in the browser from these three parts, so
// each has to arrive separately. A single pre-composed sentence could not be
// rewritten when the reader changes the period.
func TestChartIslandCarriesTheHeadingParts(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/area/silent").Body.String()

	for _, want := range []string{
		`data-t-metric="PM2.5"`,
		`data-t-tier="province median"`,
		`data-t-period-legend="Period"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the chart island is missing %s", want)
		}
	}
}
