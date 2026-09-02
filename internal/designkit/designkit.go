// Package designkit serves the design kit — the static page set the web UI is
// refined against — from a directory on disk, under /design-kit/.
//
// From disk rather than go:embed because the kit is not part of the binary's
// contract the way internal/web/static is: a mismatched app.css is a broken
// site, whereas the kit changes on its own cadence, for a different audience.
// Disk also gives an off switch. design_kit.dir = "" means the route does not
// exist at all, which is what production runs; an embedded kit cannot be
// switched off.
//
// Unlike internal/tiles this is NOT a separate listener. The kit is same-origin
// on purpose — that is what lets the app's CSP (script-src 'self') cover it
// without being relaxed, and what makes connect-src 'self' https://tiles.airbg.org
// already sufficient for the kit's own fetches. The cost is that it shares the
// public listener's token bucket, which is fine for a page a handful of people
// open and would not be for anything on the critical path.
package designkit

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// entry is the kit's entry point, relative to the served root. The kit has no
// index.html at its root and deliberately does not add one — one page, one
// owner — so /design-kit/ redirects here rather than serving a launcher.
//
// It is also the startup existence check: pointing design_kit.dir at the wrong
// level of the kit tree is the likely mis-set, and it is silent otherwise (a
// 404 on a page nobody loads until they need it).
//
// The two-level nesting is not incidental. ui_kits/app/index.html references
// ../../tokens.css, which resolves to the PROJECT ROOT — so design_kit.dir must
// be the project root and nothing below it. Pointing it at ui_kits/ instead
// serves this page and breaks every token, colour and component sheet it loads,
// which renders as an unstyled page rather than an error.
const entry = "ui_kits/app/index.html"

// entryDir is where the served root redirects. Kept beside entry because they
// must move together: a redirect to a directory whose index.html is not the
// entry point is a 404 that looks like a missing kit.
const entryDir = "ui_kits/app/"

// allowedRoots is the allowlist, applied to the first path segment.
//
// It exists because the served root has to be the project root (see entry), and
// that directory holds a great deal that is not the kit: CLAUDE.md, the 162 KB
// DESIGN.md contract, SKILL.md, working screenshots, an examples/ and a
// preview/ scratch directory, and the editor's own caches. None of it is a
// credential, and none of it is intended to be published either.
//
// An allowlist rather than a denylist for the reason denylists always fail: the
// kit is edited by a tool that creates files nobody chose, so the set of things
// that should NOT be served grows without anyone deciding to grow it. The set
// that SHOULD be served is five entries and changes when someone means it to.
//
// This subsumes the dotted-segment refusal for anything at the root — the
// editor's .file-versions/ revision history is excluded by not being listed.
// The dotted refusal is kept anyway, because it also covers every depth below.
var allowedRoots = map[string]bool{
	"ui_kits":             true,
	"assets":              true,
	"tokens.css":          true,
	"colors_and_type.css": true,
	"components.css":      true,
}

// indexFile is what a directory request resolves to. Requests for a directory
// WITHOUT one are refused rather than listed: a listing enumerates the kit's
// internals for anyone who finds the route, and it is not something any link in
// the kit asks for.
const indexFile = "index.html"

// cacheControl is short and revalidating. Nothing the kit serves is
// content-addressed — its assets are versioned by a hand-maintained ?v=NN query
// — and a hand-maintained cache-buster is one someone eventually forgets to
// bump. The failure that would cause is a reviewer looking at a stale kit and
// reporting a fixed problem, which is worse than the re-fetch this costs.
const cacheControl = "public, max-age=60, must-revalidate"

// NewHandler serves dir under /design-kit/. The caller strips that prefix.
//
// It returns an error rather than serving 404s when dir is not a kit directory,
// for the same reason tiles.NewHandler does: a mis-set path should fail at
// startup, where an operator is watching, rather than at the first request,
// where it looks like the kit itself is broken.
func NewHandler(dir string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("designkit: dir is empty")
	}
	fsys := os.DirFS(dir)
	if _, err := fs.Stat(fsys, entry); err != nil {
		return nil, fmt.Errorf("designkit: %s in %s: %w", entry, dir, err)
	}
	return &handler{fsys: fsys}, nil
}

type handler struct {
	fsys fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The served root itself. Redirect rather than serve, so the kit's relative
	// references resolve against /design-kit/ui_kits/app/ — the two-level
	// nesting the kit's ../../tokens.css depends on.
	if r.URL.Path == "" || r.URL.Path == "/" {
		// Set directly rather than via http.Redirect, which resolves a relative
		// target against the request path and would emit "/ui_kits/app/". The
		// handler runs under StripPrefix and so cannot see its own mount point:
		// after stripping, the request path IS "/", and an absolute Location
		// built from it points outside the route. A relative Location is valid
		// (RFC 9110 §10.2.2) and the browser resolves it against the full
		// request URI, giving /design-kit/ui_kits/app/ without the handler
		// knowing the prefix.
		w.Header().Set("Location", entryDir)
		w.WriteHeader(http.StatusFound)
		return
	}

	name, ok := resolve(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// Directories are never served, only their index.html. fs.Stat on the
	// resolved name catches both the missing file and the "this is a directory
	// without an index" case in one call, and refusing here is what keeps
	// ServeFileFS — which lists directories when told not to redirect — from
	// ever seeing one.
	info, err := fs.Stat(h.fsys, name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, h.fsys, name)
}

// resolve turns a request path into a name inside the served root, or reports
// that it is not one this handler will serve.
//
// Two independent bounds meet here, and neither is about traversal — os.DirFS
// already makes escaping dir structurally impossible. Both are about what sits
// INSIDE the served directory, which is an editor's working project rather than
// a curated public tree:
//
//   - allowedRoots bounds the first segment, so nothing at the root is reachable
//     unless it was named. This is the stronger of the two.
//   - the dotted-segment refusal bounds every segment at every depth, which is
//     where the editor also writes revision history (ui_kits/.file-versions/).
//
// Kept separate rather than merged because they fail differently. The allowlist
// is wrong when someone adds a kit directory and forgets to list it — a missing
// page, noticed immediately. The dotted refusal is wrong when a kit legitimately
// needs a dotted path — which has not happened and would be noticed the same
// way. Neither failure is silent, which is the property that makes two guards
// cheaper than one clever one.
func resolve(p string) (string, bool) {
	// The trailing slash is meaningful here and path.Clean removes it, so it is
	// preserved across the comparison rather than compared away. Without this
	// every directory request — including the app/ the entry redirect points at
	// — would fail the cleanliness check and 404.
	isDir := strings.HasSuffix(p, "/")
	cleaned := path.Clean(p)
	if isDir {
		if cleaned+"/" != p {
			return "", false
		}
	} else if cleaned != p {
		return "", false
	}

	trimmed := strings.TrimPrefix(cleaned, "/")
	if trimmed == "" || trimmed == "." {
		return "", false
	}
	segs := strings.Split(trimmed, "/")
	if !allowedRoots[segs[0]] {
		return "", false
	}
	for _, seg := range segs {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return "", false
		}
	}
	if isDir {
		return path.Join(trimmed, indexFile), true
	}
	return trimmed, true
}
