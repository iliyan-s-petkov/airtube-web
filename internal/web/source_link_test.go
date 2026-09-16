package web_test

import (
	"strings"
	"testing"
)

// The masthead's link to the source repository. It sits ahead of the language
// picker, which is the first control in the nav, so the two assertions that
// matter are that it points at the repository and that it comes first.
const sourceRepoURL = "https://github.com/iliyan-s-petkov/airtube-web"

func TestMastheadLinksToTheSourceRepository(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	if !strings.Contains(body, sourceRepoURL) {
		t.Fatalf("masthead has no link to %s", sourceRepoURL)
	}
}

func TestTheSourceLinkSitsLeftOfThePickers(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	src := strings.Index(body, sourceRepoURL)
	pick := strings.Index(body, `<details class="langpick">`)
	if src < 0 || pick < 0 {
		t.Fatalf("source link at %d, language picker at %d; both must render", src, pick)
	}
	if src > pick {
		t.Errorf("source link renders after the language picker; it belongs to its left")
	}
}

// An off-site link opened in a new tab hands the opener to the target unless
// it is severed, and a bare icon has no accessible name unless one is given.
func TestTheSourceLinkIsSafeAndNamed(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	start := strings.Index(body, sourceRepoURL)
	if start < 0 {
		t.Fatalf("masthead has no link to %s", sourceRepoURL)
	}
	anchor := body[strings.LastIndex(body[:start], "<a ") : start+400]
	for _, want := range []string{`rel="noopener noreferrer"`, "aria-label="} {
		if !strings.Contains(anchor, want) {
			t.Errorf("source link is missing %s", want)
		}
	}
	if !strings.Contains(anchor, "<svg") {
		t.Errorf("source link renders no icon")
	}
}
