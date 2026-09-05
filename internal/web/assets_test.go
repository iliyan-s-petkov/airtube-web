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
// A CSS-only entry carries the stylesheet as its own File and no css list.
// Without the extension check it lands in scripts and the page emits a
// <script src> pointing at a stylesheet, which loads nothing and styles
// nothing.
func TestCSSOnlyEntryResolvesAsAStyleNotAScript(t *testing.T) {
	a, err := parseManifest([]byte(
		`{"src/styles/theme.css":{"file":"assets/theme-X.css","name":"theme","isEntry":true}}`))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got, want := a.Style("theme"), "/static/build/assets/theme-X.css"; got != want {
		t.Errorf("Style(theme) = %q, want %q", got, want)
	}
	if got := a.Script("theme"); got != "" {
		t.Errorf("Script(theme) = %q, want empty: a stylesheet must not be emitted as a script", got)
	}
}

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

const themeEntryPath = "../../web/src/styles/theme.css"

var cssImport = regexp.MustCompile(`@import\s+'([^']+)'`)

// themeEntryImports resolves the theme entry's @import list against its own
// directory — the same way Vite does — so the palette tests read what the
// build actually inlines rather than a list restated here.
func themeEntryImports(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(themeEntryPath)
	if err != nil {
		t.Fatalf("ReadFile %s error = %v", themeEntryPath, err)
	}
	var out []string
	for _, m := range cssImport.FindAllStringSubmatch(string(data), -1) {
		out = append(out, filepath.Join(filepath.Dir(themeEntryPath), m[1]))
	}
	return out
}

// The site adopting the kit means the kit's files are the ones inlined. If an
// import is dropped, the tokens it defined vanish from the built palette —
// invisible unless app.css happens to use one of them, which is why this
// asserts the imports directly rather than inferring them from usage.
func TestTheThemeEntryImportsTheDesignKit(t *testing.T) {
	imports := themeEntryImports(t)
	for _, want := range []string{"tokens.css", "colors_and_type.css"} {
		found := false
		for _, got := range imports {
			if filepath.Base(got) == want && strings.Contains(got, "design-kit") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not @import design-kit/%s; the kit is meant to be the only definition", themeEntryPath, want)
		}
	}
	for _, p := range imports {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s @imports %s, which does not exist: the build would fail", themeEntryPath, p)
		}
	}
}

// The focus ring's inner band is the gap separating the accent ring from the
// page, so it has to be var(--bg). Written as a literal it needs restating in
// every dark block, and a missed one is a white halo on a dark page — a
// contrast failure on the one affordance keyboard users navigate by.
func TestTheFocusRingFollowsTheBackground(t *testing.T) {
	for _, p := range append(themeEntryImports(t), themeEntryPath) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile %s error = %v", p, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "--focus-ring:") {
				continue
			}
			if found := cssColour.FindAllString(line, -1); len(found) != 0 {
				t.Errorf("%s defines --focus-ring with the literal colour %v; use var(--bg) so it follows the theme", p, found)
			}
		}
	}
}

// In a real build the served palette is the "theme" Vite entry
// (web/src/styles/theme.css), not static/theme.css — that one is only the
// no-Node fallback. So the palette app.css is actually served with is the
// design kit plus the site-only block, and a token missing from THAT set
// fails in production while TestEveryCSSVarUsedIsDefinedInTheme still
// passes against the fallback. Five tokens were in exactly that position
// when the kit was first adopted.
func TestEveryCSSVarUsedIsDefinedInTheBuiltPalette(t *testing.T) {
	defined := map[string]bool{}
	for _, p := range append(themeEntryImports(t), themeEntryPath) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile %s error = %v", p, err)
		}
		for _, m := range cssVarDef.FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
	}
	if len(defined) == 0 {
		t.Fatal("the built palette defines no custom properties; the definition regexp no longer matches")
	}

	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile app.css error = %v", err)
	}
	for _, m := range cssVarUse.FindAllStringSubmatch(string(data), -1) {
		if !defined[m[1]] {
			t.Errorf("app.css uses %s, which the built palette (design kit + site-only block) does not define", m[1])
		}
	}
}

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
