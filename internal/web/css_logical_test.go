package web

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAppCSSUsesLogicalProperties ensures the stylesheet uses CSS logical
// properties instead of physical ones for future RTL language support.
func TestAppCSSUsesLogicalProperties(t *testing.T) {
	cssPath := filepath.Join("static", "app.css")
	data, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("could not read %s: %v", cssPath, err)
	}

	content := string(data)

	// Skip @media (max-width: ...) prelude — media features have no logical spelling.
	content = regexp.MustCompile(`@media\s*\([^)]*max-width:[^)]*\)`).ReplaceAllString(content, "")

	scanner := bufio.NewScanner(strings.NewReader(content))

	// Physical properties that should not appear in rule bodies. Match both
	// standalone lines (`  width: ...;`) and inline rules (`{ width: ... }`).
	forbiddenPatterns := map[string]*regexp.Regexp{
		"width":        regexp.MustCompile(`(?i)\bwidth\s*:`),
		"height":       regexp.MustCompile(`(?i)\bheight\s*:`),
		"min-width":    regexp.MustCompile(`(?i)\bmin-width\s*:`),
		"max-width":    regexp.MustCompile(`(?i)\bmax-width\s*:`),
		"min-height":   regexp.MustCompile(`(?i)\bmin-height\s*:`),
		"max-height":   regexp.MustCompile(`(?i)\bmax-height\s*:`),
		"margin-left":  regexp.MustCompile(`(?i)\bmargin-left\s*:`),
		"margin-right": regexp.MustCompile(`(?i)\bmargin-right\s*:`),
		"padding-left": regexp.MustCompile(`(?i)\bpadding-left\s*:`),
		"padding-right": regexp.MustCompile(`(?i)\bpadding-right\s*:`),
	}

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Check for forbidden properties.
		for prop, re := range forbiddenPatterns {
			if re.MatchString(line) {
				t.Errorf("app.css:%d: physical property %q found, use logical equivalent instead", lineNum, prop)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("error scanning %s: %v", cssPath, err)
	}
}
