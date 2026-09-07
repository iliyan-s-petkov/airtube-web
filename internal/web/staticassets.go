package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticVersionParam is the query key the templates stamp and the handler reads.
const staticVersionParam = "v"

// StaticAssets content-hashes the hand-written files under /static/.
//
// Their names are stable by necessity — app.css is referenced by hand and
// theme-init.js has to run before paint — so a deploy used to leave a visitor
// on the previous copy for the whole of the static TTL. The hash goes in the
// URL instead, which changes the cache key on every edit.
type StaticAssets struct {
	versions map[string]string // "app.css" -> hex digest prefix
}

// LoadStaticAssets hashes every embedded file under static/.
//
// A read failure leaves that file unversioned rather than failing the process:
// an unversioned URL still resolves, it just falls back to revalidating.
func LoadStaticAssets() StaticAssets {
	sa := StaticAssets{versions: make(map[string]string)}
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(staticFS, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(raw)
		sa.versions[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	return sa
}

// URL is the served path for a static file, stamped with its content hash.
func (sa StaticAssets) URL(name string) string {
	if v, ok := sa.versions[name]; ok {
		return "/static/" + name + "?" + staticVersionParam + "=" + v
	}
	return "/static/" + name
}

// version reports the current hash of name, and whether it is known.
func (sa StaticAssets) version(name string) (string, bool) {
	v, ok := sa.versions[name]
	return v, ok
}

// staticAssetCacheControl marks a request immutable only when it carries the
// hash the file currently has.
//
// Both halves matter. A stamped URL is a new URL on every edit, so a year is
// safe and no revalidation is needed. An unstamped or stale one — a bookmark,
// a link someone pasted, a page rendered by the previous build — must keep
// revalidating, or that URL pins the wrong bytes for a year.
func staticAssetCacheControl(next http.Handler, sa StaticAssets) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := shortRevalidateCacheControl
		if want, ok := sa.version(strings.TrimPrefix(path.Clean(r.URL.Path), "/static/")); ok {
			if r.URL.Query().Get(staticVersionParam) == want {
				value = immutableCacheControl
			}
		}
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}
