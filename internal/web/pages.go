package web

import (
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	staticCacheControl    = "public, max-age=3600"
)

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
	// config; without it the hashing buys nothing.
	mux.Handle("GET /static/build/", http.StripPrefix("/static/build/",
		cacheControl(noDirList(http.FileServer(http.FS(distSubFS()))), immutableCacheControl)))

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

// noDirList turns a request for a directory into a 404 before the FileServer
// can render an index of it.
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
		next.ServeHTTP(w, r)
	})
}
