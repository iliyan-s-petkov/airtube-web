package web_test

import (
	"strings"
	"testing"
)

// The sensor bar is an island container and nothing else: the filter it offers
// has no server-side meaning, so there is nothing to render as a fallback. What
// the server does owe it is the vocabulary and the translated labels.
func TestSensorBarCarriesItsVocabularyAndLabels(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/area/high").Body.String()

	for _, want := range []string{
		`data-island="sensorbar"`,
		`data-t-legend="Show"`,
		`data-t-all="All"`,
		`data-t-active="With data"`,
		`data-t-inactive="Without data"`,
		`data-t-shown="Showing"`,
		`data-t-of="of"`,
		`data-t-sensors="sensors"`,
		`data-t-silent="with no recent readings"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the area page does not carry %s", want)
		}
	}
}

// The count is per metric, so the bar needs the same metric vocabulary the map
// and the switcher get — otherwise it cannot follow a metric change.
func TestSensorBarKnowsTheMetricVocabulary(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/area/high").Body.String()

	bar := body[strings.Index(body, `data-island="sensorbar"`):]
	if !strings.Contains(bar[:strings.Index(bar, ">")], `data-metrics=`) {
		t.Error("the sensor bar is not given the metric vocabulary")
	}
	if !strings.Contains(bar[:strings.Index(bar, ">")], `data-metric=`) {
		t.Error("the sensor bar is not given the default metric")
	}
}

// On an area with no recent readings every sensor is silent, so the filter's
// other two positions would offer a choice between everything and nothing.
func TestSensorBarIsAbsentFromAnUncoveredArea(t *testing.T) {
	rr := renderer(t, fixture(t))

	if !strings.Contains(fetch(t, rr, "/area/sofia").Body.String(), `data-island="sensorbar"`) {
		t.Error("the covered area lost its sensor bar")
	}
	if strings.Contains(fetch(t, rr, "/area/vidin").Body.String(), `data-island="sensorbar"`) {
		t.Error("an uncovered area is offered a filter over sensors that all report nothing")
	}
}

// Bulgarian is the default language and the untranslated case is the one that
// ships silently — the page renders, the labels are just English.
func TestSensorBarLabelsAreTranslated(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/area/high").Body.String()

	if !strings.Contains(body, `data-t-active="С данни"`) {
		t.Error("the Bulgarian page does not carry the Bulgarian status labels")
	}
	if !strings.Contains(body, `data-t-silent="без скорошни данни"`) {
		t.Error("the Bulgarian page does not carry the Bulgarian count-line parts")
	}
}
