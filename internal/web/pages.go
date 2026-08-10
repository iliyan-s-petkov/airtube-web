package web

import (
	"net/http"
	"sort"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
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

	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

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
