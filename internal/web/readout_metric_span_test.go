package web_test

import (
	"strings"
	"testing"
)

// The metric prefix ("PM2.5 · ") is its own span so phone CSS can hide it
// without touching the i18n string that builds the rest of the label.
func TestReadoutLabelSplitsTheMetricIntoItsOwnSpan(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/").Body.String()

	if !strings.Contains(body, `<span class="readout__metric">PM2.5 · </span>`) {
		t.Fatalf("readouts strip has no readout__metric span carrying the metric prefix:\n%s", body)
	}
	// The rest of the label still prints, just outside the new span.
	if !strings.Contains(body, `</span>Highest province figure</span>`) {
		t.Errorf("the label's own text did not follow the metric span")
	}
}
