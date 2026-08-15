// Package tiles serves the self-hosted basemap: one PMTiles archive, one style
// document, and the glyph atlases MapLibre needs for labels.
//
// It exists as its own package, behind its own listener, because it must hold
// nothing. No database pool, no snapshot, no rate limiter, no admission
// semaphore. A single map load issues dozens of range requests; routed through
// the application's middleware chain they would exhaust the 10/s API bucket on
// the first pan, and raising that bucket to fit them would raise it for the
// JSON API too — the precise thing the limit exists to prevent. Bulkheads
// again: only separate resources can bound one workload's effect on another's
// capacity.
package tiles

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// required names the files NewHandler insists on finding. Glyphs are checked as
// a directory rather than by name: the fontstack depends on what the style
// references, and enumerating them here would be a second place for that choice
// to live.
var required = []string{"style.json", "bulgaria.pmtiles"}

const glyphsDir = "glyphs"

// cacheControl: the artefacts are immutable by construction — regeneration
// changes the filename (see docs/tiles.md), so a cached copy can never be
// stale. Without this every visitor re-fetches hundreds of megabytes of ranges.
const cacheControl = "public, max-age=31536000, immutable"

// NewHandler serves dir's basemap artefacts, allowing cross-origin reads from
// allowOrigin. It returns an error rather than serving 404s if dir is not a
// tile directory, so a mis-set path fails at startup instead of producing a
// blank map in production that looks like a tile-generation mistake.
func NewHandler(dir string, allowOrigin string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("tiles: dir is empty")
	}
	if allowOrigin == "" {
		// Not defaulted to "*": that would let any page on the internet read
		// the tiles, and the value we want is already known at startup.
		return nil, errors.New("tiles: allowOrigin is empty")
	}

	fsys := os.DirFS(dir)
	for _, name := range required {
		if _, err := fs.Stat(fsys, name); err != nil {
			return nil, fmt.Errorf("tiles: %s in %s: %w", name, dir, err)
		}
	}
	info, err := fs.Stat(fsys, glyphsDir)
	if err != nil {
		return nil, fmt.Errorf("tiles: %s in %s: %w", glyphsDir, dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("tiles: %s in %s is not a directory", glyphsDir, dir)
	}

	files := http.FileServerFS(fsys)
	return &handler{files: files, allowOrigin: allowOrigin}, nil
}

type handler struct {
	files       http.Handler
	allowOrigin string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set before any branch: a 404 and a 405 are cross-origin responses too,
	// and a browser that cannot read the status learns nothing from it.
	head := w.Header()
	head.Set("Access-Control-Allow-Origin", h.allowOrigin)
	head.Set("Access-Control-Allow-Headers", "Range")
	head.Set("Access-Control-Expose-Headers", "Content-Range")
	head.Set("Vary", "Origin")
	head.Set("X-Content-Type-Options", "nosniff")

	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		// Range turns every tile read into a preflighted request. Answering it
		// here rather than falling through keeps the file server from seeing a
		// method it does not handle.
		head.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		head.Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		head.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !allowed(r.URL.Path) {
		http.NotFound(w, r)
		return
	}

	head.Set("Cache-Control", cacheControl)
	h.files.ServeHTTP(w, r)
}

// allowed is the allowlist. Two independent defences meet here: os.DirFS makes
// an escape from the directory structurally impossible, and this makes anything
// else that happens to sit inside it — a build log, a half-written temp file —
// unreachable.
func allowed(p string) bool {
	// A path with "." or ".." segments is never one style.json generates; it is
	// only ever a probe. Refusing it outright is cheaper than reasoning about
	// where each one resolves to: "/glyphs/../0-255.pbf" has three segments and
	// a .pbf suffix, so the glyphs pattern below accepts it, and net/http cleans
	// it to a top-level file outside glyphs/ before opening. os.DirFS still bounds
	// it to dir, so this is not an escape — but it is a wider allowlist than the
	// one this function claims to be.
	if p != path.Clean(p) {
		return false
	}
	p = strings.TrimPrefix(p, "/")
	for _, name := range required {
		if p == name {
			return true
		}
	}
	// glyphs/{fontstack}/{range}.pbf — exactly two segments below glyphs/.
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == glyphsDir && strings.HasSuffix(parts[2], ".pbf")
}
