package web

import (
	"encoding/json"
	"io/fs"
	"path"
)

// assetPrefix is where Routes serves the embedded dist tree. The manifest holds
// paths relative to dist, so every resolved value is this plus that.
const assetPrefix = "/static/build/"

// manifestPaths are the two places Vite has put its manifest. v5 moved it into
// .vite/; the older location is kept so a downgrade is a config change rather
// than a silent loss of every script tag.
var manifestPaths = []string{".vite/manifest.json", "manifest.json"}

// Assets resolves logical entry names to hashed, served paths.
//
// The zero value is valid and resolves nothing. That is the whole graceful
// degradation story: with no manifest, the templates emit no <script>, the
// islands never load, and the page is exactly the Phase 2 page — which works.
type Assets struct {
	scripts map[string]string // entry name -> "/static/build/<hashed>.js"
	styles  map[string]string
}

// LoadAssets reads the Vite manifest out of the embedded dist tree.
//
// A missing manifest is NOT an error: it returns an empty Assets and
// found=false, so a build made on a machine with no Node serves the
// no-JavaScript site rather than failing to start. cmd/airbg logs which of the
// two happened, because otherwise a developer who forgot `npm run build` has no
// way to discover it.
func LoadAssets() (Assets, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return Assets{}, false
	}
	return loadAssetsFrom(sub)
}

// loadAssetsFrom is LoadAssets over any filesystem, so a test can drive the
// missing-manifest path without depending on whether anyone has built.
func loadAssetsFrom(fsys fs.FS) (Assets, bool) {
	for _, p := range manifestPaths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		a, err := parseManifest(raw)
		if err != nil {
			// A manifest that exists but does not parse is a real problem and
			// must not masquerade as "no manifest" — but it still must not stop
			// the process, or a bad build takes the site down instead of
			// degrading it. Reported as not-found so the startup line says so.
			return Assets{}, false
		}
		return a, true
	}
	return Assets{}, false
}

// manifestEntry is the subset of Vite's manifest shape this needs. Extra fields
// are ignored, so a Vite version that adds keys does not break the parse.
type manifestEntry struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	IsEntry bool     `json:"isEntry"`
	CSS     []string `json:"css"`
}

// parseManifest maps entry NAMES to served paths.
//
// Keyed by Name rather than by the manifest's own key: the key is a source path
// ("src/main.js") that would put a directory layout into every template, while
// Name is the logical entry ("main") the templates already want to say. Only
// isEntry records are exposed — the rest are shared chunks the browser reaches
// through the entry's own imports, and emitting a script tag for one would load
// it twice.
func parseManifest(raw []byte) (Assets, error) {
	var manifest map[string]manifestEntry
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Assets{}, err
	}
	a := Assets{
		scripts: make(map[string]string),
		styles:  make(map[string]string),
	}
	for _, e := range manifest {
		if !e.IsEntry || e.Name == "" || e.File == "" {
			continue
		}
		resolved := assetPrefix + path.Clean(e.File)
		// A CSS-only entry (the "theme" entry) has a stylesheet as its own File
		// and no CSS list. Routed by extension, because putting it in scripts
		// would emit a <script src> pointing at a stylesheet.
		if path.Ext(e.File) == ".css" {
			a.styles[e.Name] = resolved
			continue
		}
		a.scripts[e.Name] = resolved
		if len(e.CSS) > 0 {
			a.styles[e.Name] = assetPrefix + path.Clean(e.CSS[0])
		}
	}
	return a, nil
}

// Script returns the served path for an entry, or "" if unknown.
//
// "" rather than a guessed path, because the template guards on emptiness: a
// guess would emit a <script src> that 404s, which is a broken page, where ""
// is the server-rendered fallback.
func (a Assets) Script(entry string) string { return a.scripts[entry] }

// Style likewise, for the entry's bundled CSS.
func (a Assets) Style(entry string) string { return a.styles[entry] }
