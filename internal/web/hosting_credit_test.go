package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The hosting is donated, so the credit is owed on every page rather than only
// where the map is — the same footing as the data attributions beside it.
const hostingURL = "https://hostellation.com/"

func TestEveryPageCreditsTheHost(t *testing.T) {
	r := renderer(t, fixture(t))

	for _, path := range []string{"/", "/areas", "/about-the-data"} {
		body := fetch(t, r, path).Body.String()
		if !strings.Contains(body, hostingURL) {
			t.Errorf("%s does not credit the host", path)
		}
	}
}

func TestTheHostingCreditSitsInTheFooter(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	open := strings.Index(body, `<footer class="footer">`)
	shut := strings.Index(body, "</footer>")
	credit := strings.Index(body, hostingURL)
	if open < 0 || shut < open || credit < 0 {
		t.Fatalf("footer spans %d..%d, hosting credit at %d; all must render", open, shut, credit)
	}
	if credit < open || credit > shut {
		t.Errorf("hosting credit renders outside the footer")
	}
}

// Off-site, so the opener is severed. It keeps the footer's convention of
// staying in the tab: only the masthead's repository mark opens a new one.
func TestTheHostingCreditIsSafeAndNamed(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	start := strings.Index(body, hostingURL)
	if start < 0 {
		t.Fatalf("no link to %s", hostingURL)
	}
	anchor := body[strings.LastIndex(body[:start], "<a ") : start+200]
	if !strings.Contains(anchor, `rel="noopener`) {
		t.Errorf("hosting link does not sever the opener: %s", anchor)
	}
	if strings.Contains(anchor, "target=") {
		t.Errorf("hosting link opens a new tab; the footer's links do not")
	}
	if !strings.Contains(anchor, ">Hostellation<") {
		t.Errorf("hosting link is not named for the host: %s", anchor)
	}
}

// The footer's other lines are 12px captions. A donor credit that nobody can
// read is not a credit, so this one is set at the body size the disclaimer uses.
func TestTheHostingCreditIsSetAtAReadableSize(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatalf("reading app.css: %v", err)
	}

	rule := regexp.MustCompile(`\.footer \.footer__sponsor \{[^}]*\}`).Find(css)
	if rule == nil {
		t.Fatal("app.css does not size the hosting credit")
	}
	if !strings.Contains(string(rule), "var(--text-body)") {
		t.Errorf("hosting credit is not set at the body size: %s", rule)
	}
	if strings.Contains(string(rule), "var(--text-caption)") {
		t.Errorf("hosting credit is still a caption: %s", rule)
	}
}
