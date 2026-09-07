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

// A stylesheet fails silently: the browser discards whatever it cannot parse
// and paints on with the rest, so a broken comment costs a rule and no error
// anywhere. It happened — a second `*/` left the prose of a comment sitting in
// the sheet as garbage, and the CSS parser's error recovery ate the .map-tier
// rule that followed it. Nothing failed; the line simply lost its gutter, and
// it was found by measuring the live page.
//
// The two things that go wrong when comments are edited by hand are a `*/`
// with no comment open and a comment left open at EOF; the brace count catches
// a block truncated by either.
func TestAppCSSParses(t *testing.T) {
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	css := string(data)
	line, depth, comment := 1, 0, false
	for i := 0; i < len(css); i++ {
		switch {
		case css[i] == '\n':
			line++
		case comment && strings.HasPrefix(css[i:], "*/"):
			comment, i = false, i+1
		case comment:
		case strings.HasPrefix(css[i:], "/*"):
			comment, i = true, i+1
		case strings.HasPrefix(css[i:], "*/"):
			t.Fatalf("app.css:%d: `*/` with no comment open", line)
		case css[i] == '{':
			depth++
		case css[i] == '}':
			if depth--; depth < 0 {
				t.Fatalf("app.css:%d: `}` with no rule open", line)
			}
		}
	}
	if comment {
		t.Error("app.css ends inside a comment")
	}
	if depth != 0 {
		t.Errorf("app.css ends with %d rule(s) still open", depth)
	}
}

var (
	cssVarUse = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)`)
	cssVarDef = regexp.MustCompile(`(?m)^\s*(--[a-zA-Z0-9-]+)\s*:`)

	cssVarAnyDef        = regexp.MustCompile(`(?m)(?:^|[;{])\s*(--[a-zA-Z0-9-]+)\s*:`)
	cssVarUseNoFallback = regexp.MustCompile(`var\(\s*(--[a-zA-Z0-9-]+)\s*(,)?`)
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

// Importing the kit brought its prefers-color-scheme block with it, so the site
// has a dark theme whether or not anyone designed for one. The site-only tokens
// did not follow: they were light literals, and in dark mode the map overlays
// stayed white while their text went near-white — the legend, the locate button
// and the wind button all measured about 1.1:1 against their own background.
// Nothing failed; the page was simply unreadable for anyone whose OS is dark.
//
// So a site-only token may not END on a colour literal. A literal as a fallback
// before a palette-derived value is fine and is why this checks the last
// definition rather than any of them.
func TestSiteOnlyTokensFollowTheTheme(t *testing.T) {
	data, err := os.ReadFile(themeEntryPath)
	if err != nil {
		t.Fatalf("ReadFile %s error = %v", themeEntryPath, err)
	}
	last := map[string]string{}
	var order []string
	for _, line := range strings.Split(string(data), "\n") {
		m := cssVarAnyDef.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := last[m[1]]; !seen {
			order = append(order, m[1])
		}
		_, value, _ := strings.Cut(line, ":")
		last[m[1]] = value
	}
	if len(order) == 0 {
		t.Fatal("the theme entry defines no site-only tokens; the pattern no longer matches")
	}
	for _, name := range order {
		if found := cssColour.FindAllString(last[name], -1); len(found) != 0 {
			t.Errorf("%s is finally defined as the literal colour %v; derive it from a kit token so it follows the dark theme the kit ships", name, found)
		}
	}
}

// The same undefined-token failure, but inside the kit's own files rather than
// app.css. Adopting components.css brought in .place__name, whose font
// shorthand read a --text-subhead that the type scale never defined: the whole
// declaration was dropped and the name rendered at inherited body type. Only
// fallback-less uses count — var(--ramp, none) is how the kit marks a property
// the consuming app sets at runtime, and those are correct by design.
func TestTheKitUsesNoTokenTheBuiltPaletteLacks(t *testing.T) {
	imports := themeEntryImports(t)
	defined := map[string]bool{}
	for _, p := range append(imports, themeEntryPath) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile %s error = %v", p, err)
		}
		// Not cssVarDef: the kit also declares tokens inline inside a rule
		// (".map { --map-view-h: 382; }"), which an anchored pattern misses.
		for _, m := range cssVarAnyDef.FindAllStringSubmatch(string(data), -1) {
			defined[m[1]] = true
		}
	}
	for _, p := range imports {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("ReadFile %s error = %v", p, err)
		}
		for _, m := range cssVarUseNoFallback.FindAllStringSubmatch(string(data), -1) {
			if m[2] == "" && !defined[m[1]] {
				t.Errorf("%s uses %s with no fallback, and nothing in the built palette defines it: the declaration is dropped", p, m[1])
			}
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

// The kit's components.css is loaded before app.css, so app.css wins wherever
// the two set the same property — and loses, silently, wherever they set
// DIFFERENT ones. It happened on .map-hint: the kit anchors that banner to the
// bottom centre (inset-block-end + inset-inline-start:50%), app.css anchored it
// top-left, and because neither declaration overrode the other the banner ended
// up pinned to all four edges. A one-line message became a 314x465 translucent
// panel over the left third of the map, which is what a reader saw the moment
// the hint appeared.
//
// So: for a selector both files style, app.css may not anchor one edge of an
// axis the kit already anchors from the other side. Overriding the kit's own
// property is fine — that is how an override is meant to work.
// The site's own swatches follow the kit's motif: a swatch that stands for one
// map cell is a hexagon, and the scale's bands stay rectangular so the key reads
// as one continuous bar.
func TestTheSiteSwatchesFollowTheHexagonMotif(t *testing.T) {
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile app.css error = %v", err)
	}
	app := cssRulesOf(string(data))

	clip, ok := app[".legend-swatch"]["clip-path"]
	if !ok {
		t.Fatal(".legend-swatch has no clip-path: the no-data swatch must be a hexagon, like the cell it stands for")
	}
	if n := strings.Count(clip, ",") + 1; n != 6 {
		t.Errorf(".legend-swatch clip-path has %d points, want 6: %s", n, clip)
	}
	if got := app[".legend-swatch"]["height"]; got != "14px" {
		t.Errorf(".legend-swatch height = %q, want 14px so the pointy-top hexagon is not squashed", got)
	}

	for sel, decls := range app {
		if !strings.Contains(sel, "scale__band-swatch") {
			continue
		}
		if _, clipped := decls["clip-path"]; clipped {
			t.Errorf("%s is clipped: the bands must touch to form one bar", sel)
		}
	}
}

func TestAppCSSDoesNotCoAnchorAKitSelector(t *testing.T) {
	kit := cssRules(t, "../../design-kit/components.css")
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile app.css error = %v", err)
	}
	app := cssRulesOf(string(data))
	if len(kit) == 0 || len(app) == 0 {
		t.Fatal("no rules parsed from one of the sheets; the rule pattern no longer matches")
	}

	axes := []struct {
		name  string
		start []string
		end   []string
	}{
		{"block", []string{"top", "inset-block-start"}, []string{"bottom", "inset-block-end"}},
		{"inline", []string{"left", "inset-inline-start"}, []string{"right", "inset-inline-end"}},
	}
	for sel, appDecls := range app {
		kitDecls, shared := kit[sel]
		if !shared {
			continue
		}
		for _, axis := range axes {
			for _, pair := range [2][2][]string{{axis.start, axis.end}, {axis.end, axis.start}} {
				kitSide, appSide := pair[0], pair[1]
				if !anySet(kitDecls, kitSide) || !anySet(appDecls, appSide) {
					continue
				}
				// Overriding the kit's own edge back to auto is the release
				// valve, and the one this bug needed.
				if anySet(appDecls, kitSide) {
					continue
				}
				t.Errorf("app.css %s sets %v while the kit sets %v on the same %s axis: the element is anchored from both sides and stretches. Override the kit's edge (set it to auto) or drop app.css's.",
					sel, appSide, kitSide, axis.name)
			}
		}
	}
}

func anySet(decls map[string]string, props []string) bool {
	for _, p := range props {
		if _, ok := decls[p]; ok {
			return true
		}
	}
	return false
}

var cssRule = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)

func cssRules(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s error = %v", path, err)
	}
	return cssRulesOf(string(data))
}

// cssRulesOf is a selector -> property -> value index, flat: an @media block's
// inner rules land beside the top-level ones, which is what this test wants —
// a co-anchoring inside a media query stretches the box just the same.
func cssRulesOf(css string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, m := range cssRule.FindAllStringSubmatch(stripCSSComments(css), -1) {
		decls := map[string]string{}
		for _, d := range strings.Split(m[2], ";") {
			prop, value, ok := strings.Cut(d, ":")
			if !ok {
				continue
			}
			decls[strings.ToLower(strings.TrimSpace(prop))] = strings.TrimSpace(value)
		}
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.Join(strings.Fields(sel), " ")
			if sel == "" || strings.HasPrefix(sel, "@") {
				continue
			}
			if out[sel] == nil {
				out[sel] = map[string]string{}
			}
			for p, v := range decls {
				out[sel][p] = v
			}
		}
	}
	return out
}

func stripCSSComments(css string) string {
	var b strings.Builder
	for {
		i := strings.Index(css, "/*")
		if i < 0 {
			b.WriteString(css)
			return b.String()
		}
		b.WriteString(css[:i])
		j := strings.Index(css[i:], "*/")
		if j < 0 {
			return b.String()
		}
		css = css[i+j+2:]
	}
}
