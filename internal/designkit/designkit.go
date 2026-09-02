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
const entry = "app/index.html"

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
	// references resolve against /design-kit/app/ — the two-level nesting the
	// kit's ../../tokens.css depends on.
	if r.URL.Path == "" || r.URL.Path == "/" {
		// Set directly rather than via http.Redirect, which resolves a relative
		// target against the request path and would emit "/app/". The handler
		// runs under StripPrefix and so cannot see its own mount point: after
		// stripping, the request path IS "/", and an absolute Location built
		// from it points outside the route. A relative Location is valid (RFC
		// 9110 §10.2.2) and the browser resolves it against the full request
		// URI, giving /design-kit/app/ without the handler knowing the prefix.
		w.Header().Set("Location", "app/")
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
// The dotted-segment refusal is the load-bearing part. os.DirFS already makes
// escaping dir structurally impossible, so this is not about traversal: it is
// about what sits INSIDE a kit directory. The kit has no build step — the
// source tree is the deployable output — so a .git directory is a plausible
// neighbour of the files being served, and serving .git/objects/ over HTTPS
// discloses the entire history, including anything ever committed and later
// removed. Pointing design_kit.dir below any repo root is the primary control;
// this is the second one, because the primary control is a thing a human gets
// right.
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
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return "", false
		}
	}
	if isDir {
		return path.Join(trimmed, indexFile), true
	}
	return trimmed, true
}
