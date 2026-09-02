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

// styleFile is the one artefact name that is fixed. The style document is
// generated per basemap build but its name is what the renderer appends to
// tiles.public_url, so it is part of the interface rather than an operator
// choice. The archive's name is not: see tiles.archive.
//
// Glyphs are checked as a directory rather than by name: the fontstack depends
// on what the style references, and enumerating them here would be a second
// place for that choice to live.
const styleFile = "style.json"

const glyphsDir = "glyphs"

// Cache lifetimes, one per artefact, because the three artefacts are not
// addressed the same way and a single header cannot be honest about all of
// them.
//
// immutableCacheControl asks browsers to keep an artefact for a year and never
// revalidate it. That is honest ONLY for the archive, which is addressed by a
// configured, dated filename (tiles.archive, e.g. bulgaria-20260815.pmtiles):
// regenerating the basemap produces a new name, style.json points at the new
// name, and the old URL is simply never requested again. A fixed archive name
// would make this header a lie — returning visitors would keep serving
// themselves last year's map for up to a year. Without the header every visitor
// re-fetches hundreds of megabytes of ranges.
//
// styleCacheControl is short and revalidating because style.json is the one
// artefact whose NAME is fixed (see styleFile) and whose CONTENT changes on
// every regeneration — it is what points at the new archive name. Marked
// immutable it would strand every returning visitor on a cached style document
// naming an archive this handler no longer serves: the allowlist 404s any name
// but the configured one, so the map goes blank, for up to a year, and no
// server-side change can clear it. That failure arrives days after the
// regeneration that caused it, on other people's machines, which is precisely
// the kind nobody connects back to the deploy.
//
// glyphCacheControl is long but not immutable. Glyph paths are fixed too
// (glyphs/{fontstack}/{range}.pbf), so a font rebuild reuses the URLs; a day
// bounds how long a stale atlas can outlive it, while still collapsing the
// hundreds of glyph fetches a browsing session makes into one per range.
const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	styleCacheControl     = "public, max-age=300, must-revalidate"
	glyphCacheControl     = "public, max-age=86400"
)

// cacheControlFor picks the lifetime for one request path. Called after the
// allowlist, so p is already known to be the style document, the configured
// archive, or a glyph range — the default arm is unreachable and returns the
// safest of the three rather than the loudest.
func (h *handler) cacheControlFor(p string) string {
	switch strings.TrimPrefix(p, "/") {
	case styleFile:
		return styleCacheControl
	case h.archive:
		return immutableCacheControl
	default:
		return glyphCacheControl
	}
}

// NewHandler serves dir's basemap artefacts, allowing cross-origin reads from
// each origin in allowOrigins. archive is the PMTiles filename — the one
// artefact whose name changes with every basemap build, which is what keeps
// cacheControl honest.
//
// A list rather than the single origin this took before: the design kit renders
// the same basemap the app does, from a different host, and the alternative to
// naming it here is either "*" or a second copy of the archive.
//
// It returns an error rather than serving 404s if dir is not a tile directory,
// so a mis-set path — or a tiles.archive naming a file that is not there —
// fails at startup instead of producing a blank map in production that looks
// like a tile-generation mistake.
func NewHandler(dir string, archive string, allowOrigins []string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("tiles: dir is empty")
	}
	if archive == "" {
		return nil, errors.New("tiles: archive is empty")
	}
	// Defence in depth, not the primary control: os.DirFS below already bounds
	// every read to dir, so a traversing name could not escape it. This exists
	// because the failure it prevents is a confusing one — an archive name with
	// a separator would be checked here and then refused by the allowlist at
	// request time, which is a blank map, not a startup error.
	if strings.ContainsAny(archive, `/\`) || archive == "." || archive == ".." {
		return nil, fmt.Errorf("tiles: archive = %q must be a plain filename", archive)
	}
	if len(allowOrigins) == 0 {
		// Not defaulted to "*": that would let any page on the internet read
		// the tiles, and the values we want are already known at startup.
		return nil, errors.New("tiles: allowOrigins is empty")
	}
	origins := make(map[string]bool, len(allowOrigins))
	for _, o := range allowOrigins {
		if o == "" {
			return nil, errors.New("tiles: allowOrigins contains an empty origin")
		}
		// "*" is refused here as well as in config validation, because this
		// constructor is the only thing between a caller and the wildcard —
		// and a wildcard is not a wider allowlist, it is no allowlist.
		if o == "*" {
			return nil, errors.New(`tiles: allowOrigins contains "*"; name each origin`)
		}
		origins[o] = true
	}

	fsys := os.DirFS(dir)
	for _, name := range []string{styleFile, archive} {
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
	return &handler{files: files, archive: archive, origins: origins}, nil
}

type handler struct {
	files   http.Handler
	archive string
	origins map[string]bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set before any branch: a 404 and a 405 are cross-origin responses too,
	// and a browser that cannot read the status learns nothing from it.
	head := w.Header()
	// Vary unconditionally, including on the requests that get no CORS headers
	// at all. The body is the same for every origin but the headers are not, so
	// a shared cache keyed without Origin would hand an allowed origin's
	// response — ACAO included — to one that is not on the list, and the
	// allowlist would stop meaning anything. It has to be set on the misses
	// too, or the miss is the response that gets cached and replayed.
	head.Set("Vary", "Origin")
	head.Set("X-Content-Type-Options", "nosniff")
	// Echo the request's own origin rather than a configured one. A browser
	// compares ACAO to the Origin it sent, byte for byte, so returning some
	// other allowed origin is the same as returning nothing — and returning
	// nothing is exactly what an origin off the list gets. No empty header, no
	// fallback to a default: an absent ACAO is the unambiguous refusal.
	if o := r.Header.Get("Origin"); h.origins[o] {
		head.Set("Access-Control-Allow-Origin", o)
		head.Set("Access-Control-Allow-Headers", "Range")
		// Content-Length as well as Content-Range: PMTiles reads the archive by
		// range, and a range response the script can receive but not measure is
		// not one it can parse.
		head.Set("Access-Control-Expose-Headers", "Content-Range, Content-Length")
	}

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

	if !h.allowed(r.URL.Path) {
		http.NotFound(w, r)
		return
	}

	head.Set("Cache-Control", h.cacheControlFor(r.URL.Path))
	h.files.ServeHTTP(w, r)
}

// allowed is the allowlist. Two independent defences meet here: os.DirFS makes
// an escape from the directory structurally impossible, and this makes anything
// else that happens to sit inside it — a build log, a half-written temp file,
// last year's archive left behind after a regeneration — unreachable.
//
// It is still exactly one archive name, as before: the name is now the
// configured one rather than a compiled-in one, which is what lets the dated
// filenames docs/tiles.md tells the operator to generate actually be served.
func (h *handler) allowed(p string) bool {
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
	if p == styleFile || p == h.archive {
		return true
	}
	// glyphs/{fontstack}/{range}.pbf — exactly two segments below glyphs/.
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == glyphsDir && strings.HasSuffix(parts[2], ".pbf")
}
