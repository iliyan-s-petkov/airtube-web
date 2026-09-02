package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestLoadAssetsResolvesTheEntryFromTheManifest is the test that catches a
// manifest-format change on a Vite upgrade. The hashed filename is not
// knowable from Go; the manifest is the only contract between the two build
// systems, and a silently renamed field means a page that ships no script tag
// with no error anywhere.
func TestLoadAssetsResolvesTheEntryFromTheManifest(t *testing.T) {
	a, found := LoadAssets()
	if !found {
		t.Skip("no manifest embedded; run `npm run build` in web/ to exercise this path")
	}
	got := a.Script("main")
	if got == "" {
		t.Fatal(`Script("main") = "", want the hashed path from the manifest`)
	}
	if !strings.HasPrefix(got, "/static/build/") {
		t.Errorf("Script(\"main\") = %q, want a /static/build/ prefix", got)
	}
	if strings.Contains(got, "..") {
		t.Errorf("Script(\"main\") = %q, contains a traversal segment", got)
	}
}

// TestParseManifestReadsTheViteShape works on a fixture rather than the
// embedded tree, so it runs on a machine with no Node and pins the field names
// independently of whether anyone has built.
func TestParseManifestReadsTheViteShape(t *testing.T) {
	const fixture = `{
	  "src/main.js": {
	    "file": "assets/main-DEADBEEF.js",
	    "name": "main",
	    "src": "src/main.js",
	    "isEntry": true,
	    "css": ["assets/main-CAFEBABE.css"]
	  }
	}`
	a, err := parseManifest([]byte(fixture))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got, want := a.Script("main"), "/static/build/assets/main-DEADBEEF.js"; got != want {
		t.Errorf("Script(\"main\") = %q, want %q", got, want)
	}
	if got, want := a.Style("main"), "/static/build/assets/main-CAFEBABE.css"; got != want {
		t.Errorf("Style(\"main\") = %q, want %q", got, want)
	}
}

// TestUnknownEntryResolvesToEmpty. The template guards on the empty string, so
// an unknown entry must be "" and never a plausible-looking guessed path: a
// guessed path is a 404 and a broken page, while "" is the fallback the whole
// .keep design exists to preserve.
func TestUnknownEntryResolvesToEmpty(t *testing.T) {
	a, _ := parseManifest([]byte(`{"src/main.js":{"file":"assets/main-X.js","name":"main","isEntry":true}}`))
	if got := a.Script("chart"); got != "" {
		t.Errorf("Script(\"chart\") = %q, want \"\"", got)
	}
	if got := a.Style("main"); got != "" {
		t.Errorf("Style(\"main\") = %q, want \"\" (the fixture declares no css)", got)
	}
	var zero Assets
	if got := zero.Script("main"); got != "" {
		t.Errorf("zero Assets Script = %q, want \"\" — the zero value must resolve nothing", got)
	}
}

// TestLoadAssetsWithNoManifestReportsNotFound is the graceful-degradation
// contract. Deliberately does not assert on the embedded tree, which may or may
// not contain a build: it drives parseManifest's caller through the
// missing-file path directly.
func TestLoadAssetsWithNoManifestReportsNotFound(t *testing.T) {
	dir := t.TempDir()
	// An empty directory stands in for a dist tree with only .keep in it.
	if err := os.WriteFile(filepath.Join(dir, ".keep"), nil, 0o644); err != nil {
		t.Fatalf("write .keep: %v", err)
	}
	a, found := loadAssetsFrom(os.DirFS(dir))
	if found {
		t.Error("found = true with no manifest present")
	}
	if got := a.Script("main"); got != "" {
		t.Errorf("Script(\"main\") = %q, want \"\" when no manifest exists", got)
	}
}

// TestNonEntryChunkIsNotExposed: a shared chunk in the manifest (isEntry
// false, or absent) must never surface as a resolvable Script/Style — the
// browser reaches it through the entry's own import graph, and emitting a
// second script tag for it would load it twice.
func TestNonEntryChunkIsNotExposed(t *testing.T) {
	const fixture = `{
	  "src/main.js": {
	    "file": "assets/main-DEADBEEF.js",
	    "name": "main",
	    "src": "src/main.js",
	    "isEntry": true
	  },
	  "_shared-chunk.js": {
	    "file": "assets/shared-X.js",
	    "name": "shared"
	  }
	}`
	a, err := parseManifest([]byte(fixture))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got := a.Script("shared"); got != "" {
		t.Errorf("Script(\"shared\") = %q, want \"\" — non-entry chunks must not be exposed", got)
	}
}

// cssColour matches colour syntax only — three or six (or more) hex digits
// after a '#', or an rgb()/rgba() function call — so it does not also flag
// app.css's ID selectors (#map, #chart) or its comments.
var cssColour = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(`)

// app.css must hold no literal colours: they belong in theme.css, which is the
// one file a retheme touches. A hex literal here is a colour that silently
// escapes the palette.
func TestAppCSSHasNoLiteralColours(t *testing.T) {
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if found := cssColour.FindAllString(string(data), -1); len(found) != 0 {
		t.Errorf("app.css contains literal colours %v; they belong in theme.css as custom properties", found)
	}
}

var (
	cssVarUse = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)`)
	cssVarDef = regexp.MustCompile(`(?m)^\s*(--[a-zA-Z0-9-]+)\s*:`)
)

// The other half of the theme split: TestAppCSSHasNoLiteralColours stops a
// colour from escaping the palette, and this stops a token from being consumed
// that the palette never defines. Neither failure is visible in a browser — an
// undefined custom property with no fallback makes the whole declaration
// invalid, so the rule is dropped silently and the element renders unstyled
// rather than wrong. That is the failure mode a retheme is most likely to hit,
// because dropping a token is invisible in the diff of the file that defines it.
func TestEveryCSSVarUsedIsDefinedInTheme(t *testing.T) {
	theme, err := staticFS.ReadFile("static/theme.css")
	if err != nil {
		t.Fatalf("ReadFile theme.css error = %v", err)
	}
	defined := map[string]bool{}
	for _, m := range cssVarDef.FindAllStringSubmatch(string(theme), -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("theme.css defines no custom properties; the definition regexp no longer matches the file")
	}

	// Every hand-written stylesheet the server embeds, not just app.css:
	// theme.css itself is included so a token defined in terms of another
	// token cannot reference one that does not exist.
	for _, name := range []string{"static/app.css", "static/theme.css"} {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile %s error = %v", name, err)
		}
		for _, m := range cssVarUse.FindAllStringSubmatch(string(data), -1) {
			if !defined[m[1]] {
				t.Errorf("%s uses %s, which theme.css does not define", name, m[1])
			}
		}
	}
}
