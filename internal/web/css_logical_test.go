package web

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A declaration only starts at the beginning of a line or just after `{` or
// `;`. Anchoring there is what keeps `line-height`, `stroke-width`,
// `font-weight` and `border-width` out of the match: \b would not, because a
// hyphen is a non-word character and \bheight matches inside `line-height`.
// The same anchoring is what lets bare `left`/`right` be listed without also
// matching `margin-left` or the `-right` in a MapLibre control class name.
// `top`/`bottom` are block-axis and have no inline-logical equivalent, so they
// stay physical and are deliberately absent.
var forbiddenDeclaration = regexp.MustCompile(
	`(?i)(?:^|[{;])\s*((?:min-|max-)?(?:width|height)|(?:margin|padding|inset)-(?:left|right)|left|right)\s*:`)

// Media features have no logical spelling, so a prelude is not a declaration.
var mediaPrelude = regexp.MustCompile(`@media[^{]*`)

// TestAppCSSUsesLogicalProperties keeps the stylesheet on logical properties so
// a right-to-left language stays a translation rather than a second stylesheet.
func TestAppCSSUsesLogicalProperties(t *testing.T) {
	cssPath := filepath.Join("static", "app.css")
	data, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", cssPath, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := mediaPrelude.ReplaceAllString(scanner.Text(), "")
		for _, m := range forbiddenDeclaration.FindAllStringSubmatch(line, -1) {
			t.Errorf("app.css:%d: physical property %q — use the logical equivalent (inline-size/block-size, margin-inline-*, padding-inline-*, inset-inline-*)", lineNum, m[1])
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("error scanning %s: %v", cssPath, err)
	}
}
