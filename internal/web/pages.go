package web

import (
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	staticCacheControl    = "public, max-age=3600"

	// shortRevalidateCacheControl is used only for mapLibreUnhashedAssets — see
	// the comment there for why those two files cannot share immutableCacheControl
	// with the rest of the build tree.
	shortRevalidateCacheControl = "public, max-age=300, must-revalidate"
)

// mapLibreUnhashedAssets names the two files web/vite.config.js's
// copyMapLibreWorker plugin copies into dist/assets/ under fixed, unhashed
// names (kept in sync by hand with that plugin's own list — there is no
// build-time link between the two, so a rename on either side needs both
// updated). They are unhashed by necessity, not oversight: MapLibre resolves
// each of them itself at runtime via `new URL('./name.mjs', import.meta.url)`,
// so the request URL has to stay fixed across a maplibre-gl version bump.
//
// That is exactly why they cannot be served with the immutable, one-year
// Cache-Control the rest of /static/build/ gets. A version bump changes their
// content without changing their URL; a returning visitor with the old pair
// cached would get a mismatched worker for up to a year, and the failure is
// silent — no console error, the map's style/source simply never finishes
// loading. A short, revalidating TTL bounds that window instead.
var mapLibreUnhashedAssets = []string{
	"maplibre-gl-worker.mjs",
	"maplibre-gl-shared.mjs",
}

// Routes returns the page routes plus the embedded static assets.
//
// The language prefix is handled by registering each pattern twice rather than
// by a rewriting middleware: ServeMux then owns the matching, {slug} is parsed
// by the same code for both languages, and there is no path-mangling step where
// "/energy" could be mistaken for English.
func (rr *Renderer) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	for _, prefix := range []string{"", "/en"} {
		root := prefix + "/"
		if prefix == "" {
			root = "/{$}" // exact "/" only, so it does not swallow every path
		} else {
			root = prefix + "/{$}"
		}
		mux.HandleFunc("GET "+root, rr.handleIndex)
		mux.HandleFunc("GET "+prefix+"/areas", rr.handleIndex)
		mux.HandleFunc("GET "+prefix+"/area/{slug}", rr.handleArea)
	}

	// Content-hashed bundles: cacheable forever, because the name changes when
	// the content does. This is the payoff for `manifest: true` in the Vite
	// config; without it the hashing buys nothing. The two MapLibre worker
	// files under the same tree are the deliberate exception — see
	// buildAssetCacheControl and mapLibreUnhashedAssets.
	mux.Handle("GET /static/build/", http.StripPrefix("/static/build/",
		noDirList(buildAssetCacheControl(http.FileServer(http.FS(distSubFS()))))))

	// Hand-written CSS keeps a stable name, so it gets a short TTL instead. An
	// immutable header here would pin an edited stylesheet in every visitor's
	// browser for a year.
	mux.Handle("GET /static/", cacheControl(noDirList(http.FileServer(http.FS(staticFS))), staticCacheControl))

	// Anything unmatched is a rendered 404, not net/http's bare text one.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
	})

	return mux
}

func (rr *Renderer) handleIndex(w http.ResponseWriter, r *http.Request) {
	snap := rr.holder.Load()
	if snap == nil {
		rr.RenderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}

	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	data.Areas = areaRows(snap, lang, "oblast")
	rr.render(w, http.StatusOK, "index", data)
}

func (rr *Renderer) handleArea(w http.ResponseWriter, r *http.Request) {
	snap := rr.holder.Load()
	if snap == nil {
		rr.RenderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}

	// Validated against the snapshot, so no caller-supplied slug is ever used
	// for anything but a map lookup.
	meta, ok := snap.KnownSlugs[r.PathValue("slug")]
	if !ok {
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
		return
	}

	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	row := rowFrom(meta, lang)
	data.Area = &row
	rr.render(w, http.StatusOK, "area", data)
}

func areaRows(snap *snapshot.Snapshot, lang, kind string) []AreaRow {
	rows := make([]AreaRow, 0, len(snap.KnownSlugs))
	for _, meta := range snap.KnownSlugs {
		if kind != "" && meta.Kind != kind {
			continue
		}
		rows = append(rows, rowFrom(meta, lang))
	}
	// Sorted by name so the list is stable between requests. Map iteration
	// order would reshuffle it on every page load — visibly wrong to a reader
	// and pointless cache churn at the edge.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func rowFrom(meta snapshot.AreaMeta, lang string) AreaRow {
	name := meta.NameBG
	if lang == "en" && meta.NameEN != "" {
		name = meta.NameEN
	}
	return AreaRow{
		Slug: meta.Slug, Name: name, Kind: meta.Kind,
		Lon: meta.CentroidLon, Lat: meta.CentroidLat, Zoom: meta.DefaultZoom,
		Covered: meta.Covered, SensorCount: meta.SensorCount,
	}
}

// distSubFS strips the "dist" prefix so /static/build/assets/x.js maps to
// dist/assets/x.js. The error is impossible — the directory is embedded, so it
// exists — and an empty FS would serve 404s rather than panic, which is the
// correct degradation for a path that only serves optional bundles.
func distSubFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return distFS
	}
	return sub
}

// cacheControl sets the header before delegating, so a FileServer that writes
// its own headers cannot clobber it.
func cacheControl(next http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// buildAssetCacheControl is cacheControl specialised for /static/build/: every
// asset gets immutableCacheControl except the exact basenames listed in
// mapLibreUnhashedAssets, which get shortRevalidateCacheControl instead.
//
// Matched on the exact basename, not a prefix or a ".mjs" extension glob.
// Vite hashes some of its own emitted chunks with a ".mjs" extension too — an
// extension or prefix match would silently strip immutable caching from those
// as well, which is a correctness regression this function exists to avoid,
// not just an unrelated cleanliness concern.
func buildAssetCacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := immutableCacheControl
		base := path.Base(r.URL.Path)
		for _, name := range mapLibreUnhashedAssets {
			if base == name {
				value = shortRevalidateCacheControl
				break
			}
		}
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// noDirList turns a request for a directory, or for any dot-prefixed path, into
// a 404 before the FileServer can serve it.
//
// Checked by path shape rather than by stat-ing the filesystem: a trailing
// slash is the only way http.FileServer serves a listing (it redirects
// "/dir" to "/dir/" first), so refusing the slash refuses the listing without a
// second filesystem lookup. An empty path — the prefix-stripped form of
// "/static/build/" — is the root directory and gets the same treatment.
func noDirList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if hasDotSegment(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hasDotSegment reports whether any "/"-separated segment of p begins with a
// dot.
//
// The embed directive for the build tree is `//go:embed all:dist`, and the
// `all:` prefix is not incidental — it is what makes the committed
// dist/.keep sentinel match the pattern, and an embed pattern that matches
// nothing is a compile error. The side effect is that Vite's own
// .vite/manifest.json is embedded and served too: a fixed URL, publishing the
// entire chunk graph (including chunks no page references), whose content
// changes on every build while the response carries a one-year immutable
// Cache-Control. That is both an information leak and an incoherently cached
// response, so the whole class is refused here rather than the two names that
// exist today.
//
// Matched per SEGMENT, on a LEADING dot — not with strings.Contains(p, "/.")
// (equivalent here, but it invites the sloppier Contains(p, ".") next to it)
// and emphatically not on "contains a dot": every content-hashed asset name
// (main-BFfKsolS.js, map-CKRTiAqP.css) contains dots, and rejecting those
// would 404 the entire application bundle. No legitimate Vite output has a
// dot-prefixed path segment.
func hasDotSegment(p string) bool {
	for _, segment := range strings.Split(p, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}
