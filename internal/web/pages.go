package web

import (
	"io/fs"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

const (
	immutableCacheControl = "public, max-age=31536000, immutable"

	// shortRevalidateCacheControl is what a /static/ URL gets when it arrives without
	// the current content stamp (see staticAssetCacheControl).
	shortRevalidateCacheControl = "public, max-age=300, must-revalidate"
)

// Routes returns the page routes plus the embedded static assets.
//
// The language prefix is handled by registering each pattern twice rather than
// by a rewriting middleware: ServeMux then owns the matching, {slug} is parsed
// by the same code for both languages, and there is no path-mangling step where
// "/energy" could be mistaken for English.
func (rr *Renderer) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	for pattern, h := range rr.handlers() {
		if pattern == "/" {
			continue // catchAll needs the mux to test slash-stripped paths
		}
		mux.Handle(pattern, h)
	}
	mux.Handle("/", rr.catchAll(mux))
	return mux
}

// RoutePatterns lists every pattern Routes registers, in no particular order.
// Built from the same map Routes ranges over, so OpenProject #584's privacy
// guard test enumerates the real surface instead of a second, driftable list.
func (rr *Renderer) RoutePatterns() []string {
	h := rr.handlers()
	patterns := make([]string, 0, len(h))
	for pattern := range h {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

// handlers is the single source of truth for the page router: pattern to
// handler. Routes registers it on a mux; RoutePatterns lists its keys.
func (rr *Renderer) handlers() map[string]http.Handler {
	h := map[string]http.Handler{}

	// One prefix per loaded language: "" for the default, "/<lang>" for the
	// rest. Derived from the catalogue rather than written out, so a language
	// dropped into i18n.dir is routable without a code change — a hardcoded
	// {"", "/en"} would load de.json, list German in the switcher, and then
	// 404 every /de/ link it rendered.
	for _, lang := range rr.cat.Languages() {
		prefix := ""
		if lang != i18n.DefaultLang {
			prefix = "/" + lang
		}
		root := prefix + "/{$}" // exact match only, so it does not swallow every path
		h["GET "+root] = http.HandlerFunc(rr.handleIndex)
		if prefix != "" {
			// The language home is canonical with its slash; ServeMux's own
			// redirect from the bare prefix is a 307.
			h["GET "+prefix] = http.RedirectHandler(prefix+"/", http.StatusMovedPermanently)
		}
		h["GET "+prefix+"/areas"] = http.HandlerFunc(rr.handleIndex)
		h["GET "+prefix+"/area/{slug}"] = http.HandlerFunc(rr.handleArea)
		h["GET "+prefix+"/about"] = http.HandlerFunc(rr.handleAboutProject)
		h["GET "+prefix+"/about-the-data"] = http.HandlerFunc(rr.handleAbout)
		h["GET "+prefix+"/privacy"] = http.HandlerFunc(rr.handlePrivacy)
		h["GET "+prefix+"/licences"] = http.HandlerFunc(rr.handleLicences)
		h["GET "+prefix+"/embed"] = http.HandlerFunc(rr.handleEmbed)
	}

	// One robots.txt and one sitemap for all languages.
	h["GET /robots.txt"] = http.HandlerFunc(rr.handleRobots)
	h["GET /sitemap.xml"] = http.HandlerFunc(rr.handleSitemap)

	// Content-hashed bundles: cacheable forever, because the name changes when
	// the content does. This is the payoff for `manifest: true` in the Vite
	// config; without it the hashing buys nothing. The MapLibre worker files are
	// hashed by the copy-maplibre-gl-worker plugin, so they fall under it too.
	h["GET /static/build/"] = http.StripPrefix("/static/build/",
		noDirList(buildAssetCacheControl(http.FileServer(http.FS(distSubFS())))))

	// Hand-written files keep stable names, so the templates stamp them with a
	// content hash instead and this decides the TTL from that stamp — see
	// staticAssetCacheControl.
	h["GET /static/"] = staticAssetCacheControl(noDirList(serveStaticFiles(rr.static, staticFS)), rr.static)

	// Browsers and crawlers ask for this path regardless of <link rel=icon>.
	h["GET /favicon.ico"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, rr.static.URL("favicon.svg"), http.StatusMovedPermanently)
	})

	// Anything unmatched is a rendered 404, not net/http's bare text one.
	// Routes swaps this for catchAll, which also handles trailing slashes.
	h["/"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
	})

	return h
}

func (rr *Renderer) handleIndex(w http.ResponseWriter, r *http.Request) {
	snap := rr.holder.Load()
	if snap == nil {
		rr.RenderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}

	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	data.Areas = rr.areaRows(snap, lang, "oblast")
	// From 1024px the map's panel carries the open sensor; app.css hides the card under the map.
	data.PanelHostClass = "place-host--docked"
	// /areas shares this handler with / — same snapshot, same rows — but the
	// two are different search results and must not share a title (OpenProject
	// #605, #602 audit finding a).
	if path == "/areas" {
		data.Title = composeTitle(rr.cat.T(lang, "seo.areas.title"), rr.cat.T(lang, "seo.title_brand"))
		data.Description = rr.cat.T(lang, "seo.areas.description")
		// Only on /areas, per §3 of the SEO6 plan: it keeps / — the worst LCP
		// on the site — from paying for 79 extra links it does not render.
		data.Directory = rr.buildDirectory(snap, lang)
	} else {
		data.Title = composeTitle(rr.cat.T(lang, "seo.home.title"), rr.cat.T(lang, "seo.title_brand"))
		data.Description = rr.cat.T(lang, "seo.home.description")
		data.JSONLD = rr.mustJSONLD(data.homeJSONLD())
	}
	rr.render(w, r, http.StatusOK, "index", data)
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

	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	row := rr.rowFrom(meta, lang)
	data.Area = &row
	data.Title, data.Description = rr.areaSEO(row, lang)
	data.AreaNowHTML = data.areaNowHTML(row)
	data.AreaDayHTML = data.areaDayHTML(row)
	rr.buildAreaLinks(&data, meta, row, snap, lang)
	data.JSONLD = rr.mustJSONLD(rr.areaJSONLD(data, row))
	rr.render(w, r, http.StatusOK, "area", data)
}

// areaSEO builds the title and description for one area page, per kind — the
// three shapes seo-copy.md §2/§4 defines. Static descriptions only (no live
// numbers): OpenProject #605 ships the always-true variant, the live one
// behind a flag later.
func (rr *Renderer) areaSEO(row AreaRow, lang string) (title, description string) {
	brand := rr.cat.T(lang, "seo.title_brand")
	switch row.Kind {
	case "oblast":
		label := rr.oblastForm(row.Slug, lang, row.Name, "seo.oblast.label")
		inline := rr.oblastForm(row.Slug, lang, row.Name, "seo.oblast.inline")
		core := strings.ReplaceAll(rr.cat.T(lang, "seo.oblast.title"), "{label}", label)
		desc := strings.ReplaceAll(rr.cat.T(lang, row.descKey("seo.oblast.description")), "{inline}", inline)
		return composeTitle(core, brand), desc
	case "city":
		core := strings.ReplaceAll(rr.cat.T(lang, "seo.city.title"), "{name}", row.Name)
		desc := strings.ReplaceAll(rr.cat.T(lang, row.descKey("seo.city.description")), "{name}", row.Name)
		return composeTitle(core, brand), desc
	default: // "neighbourhood" — the 24 Sofia districts; no other kind exists.
		core := strings.ReplaceAll(rr.cat.T(lang, "seo.district.title"), "{name}", row.Name)
		desc := strings.ReplaceAll(rr.cat.T(lang, "seo.district.description"), "{name}", row.Name)
		return composeTitle(core, brand), desc
	}
}

// hasEEA reports whether official (eea) data reaches this area, so its copy
// may name the official stations; citizen-only areas never do.
func (r AreaRow) hasEEA() bool {
	if r.Source == "eea" {
		return true
	}
	_, ok := r.BySource["eea"]
	return ok
}

// descKey picks the "_eea" description variant for areas with official data.
func (r AreaRow) descKey(base string) string {
	if r.hasEEA() {
		return base + "_eea"
	}
	return base
}

// oblastForm resolves an oblast's label or inline form: the per-slug override
// key when the catalogue has one (sofiyska-oblast, sofiya-grad-oblast — the
// two whose surface form isn't "{base key} {name}" — see seo-copy.md §0), the
// templated default otherwise.
func (rr *Renderer) oblastForm(slug, lang, name, baseKey string) string {
	if key := baseKey + "." + slug; rr.cat.Has(lang, key) {
		return rr.cat.T(lang, key)
	}
	return strings.ReplaceAll(rr.cat.T(lang, baseKey), "{name}", name)
}

// handleAbout serves the data caveats — what the map does not tell you on the
// page: that 14 of the 27 city boundaries are whole municipalities, that
// coverage is uneven, that these are low-cost sensors and not reference
// instruments. Every one of those changes how a number on this site should be
// read, and until now they lived only in docs/known-limitations.md, a repo
// file no visitor will ever open.
//
// Unlike every other page here it does NOT 503 when the snapshot is missing.
// The content is static prose that needs no snapshot, and the moment a reader
// is most likely to go looking for "is this site trustworthy" is the moment
// the data is not loading. Returning the timestamp when a snapshot happens to
// exist keeps the footer consistent with the rest of the site; a zero time
// renders no timestamp line at all (see base.gohtml).
func (rr *Renderer) handleAbout(w http.ResponseWriter, r *http.Request) {
	var generatedAt time.Time
	if snap := rr.holder.Load(); snap != nil {
		generatedAt = snap.GeneratedAt
	}

	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, generatedAt)
	data.Title = composeTitle(rr.cat.T(lang, "seo.about_data.title"), rr.cat.T(lang, "seo.title_brand"))
	data.Description = rr.cat.T(lang, "seo.about_data.description")
	data.JSONLD = rr.mustJSONLD(data.datasetJSONLD())
	// The privacy section (OpenProject #585): every key comes from the
	// embedded allow-list, never hand-typed here — see storagekeys.go.
	data.StorageKeys = storageKeyInfos(rr.cat, lang)
	data.StorageKeysCSV = storageKeysCSV()
	rr.render(w, r, http.StatusOK, "about", data)
}

// handleAboutProject serves /about: what the project is, how to start, privacy
// and the visitors chart. Like /about-the-data it needs no snapshot, so it
// renders when the data is not loading.
func (rr *Renderer) handleAboutProject(w http.ResponseWriter, r *http.Request) {
	var generatedAt time.Time
	if snap := rr.holder.Load(); snap != nil {
		generatedAt = snap.GeneratedAt
	}

	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, generatedAt)
	data.Title = composeTitle(rr.cat.T(lang, "seo.about.title"), rr.cat.T(lang, "seo.title_brand"))
	data.Description = rr.cat.T(lang, "seo.about.description")
	data.StorageKeys = storageKeyInfos(rr.cat, lang)
	data.StorageKeysCSV = storageKeysCSV()
	rr.render(w, r, http.StatusOK, "about_project", data)
}

// handlePrivacy serves /privacy. Static prose like /about: no snapshot needed.
func (rr *Renderer) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	data := rr.staticPageData(r, "seo.privacy")
	data.StorageKeys = storageKeyInfos(rr.cat, data.Lang)
	data.StorageKeysCSV = storageKeysCSV()
	rr.render(w, r, http.StatusOK, "privacy", data)
}

// handleLicences serves /licences: every data source and the code licence.
func (rr *Renderer) handleLicences(w http.ResponseWriter, r *http.Request) {
	rr.render(w, r, http.StatusOK, "licences", rr.staticPageData(r, "seo.licences"))
}

// staticPageData is the shared setup of the snapshot-free prose pages; keyBase
// names the catalogue's <keyBase>.title and <keyBase>.description.
func (rr *Renderer) staticPageData(r *http.Request, keyBase string) PageData {
	var generatedAt time.Time
	if snap := rr.holder.Load(); snap != nil {
		generatedAt = snap.GeneratedAt
	}
	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, generatedAt)
	data.Title = composeTitle(rr.cat.T(lang, keyBase+".title"), rr.cat.T(lang, "seo.title_brand"))
	data.Description = rr.cat.T(lang, keyBase+".description")
	return data
}

// handleEmbed serves the map on its own, for an <iframe> on someone else's
// site. Two things make it different from every other route here, and both are
// stated in the response rather than left to a proxy: it lifts frame-ancestors
// (see httpx.EmbedCSP) and it asks not to be indexed, because the page it
// mirrors is /.
//
// Like /about-the-data it renders with no snapshot: the map island fetches its
// own data, so a snapshot gap is a map that fills in a moment later rather than
// a 503 inside a partner's page.
//
// Both query parameters are validated against what the server already knows —
// the metric list and the snapshot's slugs — so nothing a host page writes
// reaches a template or a query as a value of its own.
func (rr *Renderer) handleEmbed(w http.ResponseWriter, r *http.Request) {
	var generatedAt time.Time
	snap := rr.holder.Load()
	if snap != nil {
		generatedAt = snap.GeneratedAt
	}

	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, generatedAt)
	// The one real difference in the sensor card's host markup across the
	// three pages that mount it — see PanelHostClass and "sensorCardHost".
	data.PanelHostClass = "embed__panel"

	query := r.URL.Query()
	if metric := query.Get("metric"); slices.Contains(data.Metrics, metric) {
		data.DefaultMetric = metric
	}
	if slug := query.Get("area"); slug != "" && snap != nil {
		if meta, ok := snap.KnownSlugs[slug]; ok {
			data.DefaultLon, data.DefaultLat = meta.CentroidLon, meta.CentroidLat
			data.DefaultZoom = meta.DefaultZoom
		}
	}

	h := w.Header()
	h.Set("Content-Security-Policy", rr.embedCSP)
	// Set by SecurityHeaders before this handler runs, and it has no allowlist
	// form — for a framed route the CSP directive above is the whole policy.
	h.Del("X-Frame-Options")
	h.Set("X-Robots-Tag", "noindex")
	rr.render(w, r, http.StatusOK, "embed", data)
}

func (rr *Renderer) areaRows(snap *snapshot.Snapshot, lang, kind string) []AreaRow {
	rows := make([]AreaRow, 0, len(snap.KnownSlugs))
	for _, meta := range snap.KnownSlugs {
		if kind != "" && meta.Kind != kind {
			continue
		}
		rows = append(rows, rr.rowFrom(meta, lang))
	}
	// Ranked by the reading, because a reader opens this page to find out
	// where the air is bad — not to read an alphabet. Three tiers, in order:
	//
	//  1. Areas with a value, highest first. That is the question being asked.
	//  2. Ties broken by name, and areas WITHOUT a value sorted by name among
	//     themselves. The original rationale still holds and is not dropped:
	//     map iteration order would reshuffle the list on every page load,
	//     visibly wrong to a reader and pointless cache churn at the edge. A
	//     total order is what keeps the page byte-identical between requests.
	//  3. Areas with no reading last. They answer nothing about the air, so
	//     they do not belong above an area that does — the same call the
	//     design contract makes for the table's silent rows. Their sensor
	//     count still prints, so absence is stated rather than hidden.
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.HasValue != b.HasValue {
			return a.HasValue
		}
		if a.HasValue && a.Value != b.Value {
			return a.Value > b.Value
		}
		return a.Name < b.Name
	})
	return rows
}

// rowFrom picks the area's name for lang without needing a database column per
// language.
//
// Order: a catalogue key "area.name.<slug>" if that language's catalogue has
// one, then name_en for any non-default language, then name_bg.
//
// The middle rule is what makes a third language usable on day one: a German
// reader is better served by "Sofia" than by "София", so name_en acts as the
// Latin-script fallback until someone writes area.name.sofia into de.json. The
// catalogue key wins over both, so a translator can correct any name they
// disagree with — one line of JSON, no migration, no rebuild.
func (rr *Renderer) rowFrom(meta snapshot.AreaMeta, lang string) AreaRow {
	name := meta.NameBG
	if lang != i18n.DefaultLang && meta.NameEN != "" {
		name = meta.NameEN
	}
	if key := "area.name." + meta.Slug; rr.cat.Has(lang, key) {
		name = rr.cat.T(lang, key)
	}
	// The default metric is the one the rest of the page already renders, so
	// the list and the map agree without a second policy to keep in step.
	value, hasValue := meta.Values[rr.defaultMetric]
	valueText := ""
	if hasValue {
		valueText = formatValue(value, lang)
	}
	colour := ""
	if hasValue {
		colour = bandColour(rr.defaultMetric, value)
	}
	return AreaRow{
		Slug: meta.Slug, Name: name, Kind: meta.Kind,
		Lon: meta.CentroidLon, Lat: meta.CentroidLat, Zoom: meta.DefaultZoom,
		Covered: meta.Covered, SensorCount: meta.SensorCount,
		Value: value, HasValue: hasValue, ValueText: valueText,
		Colour: colour, Values: meta.Values,
		Source: meta.Source, BySource: meta.BySource,
		ParentSlug: meta.ParentSlug, Day: meta.Day,
	}
}

// formatValue writes a reading the way the language writes a decimal:
// Bulgarian puts a comma where English puts a point. One place, because the
// province rows and the country readouts sit on the same screen and printing
// 12,4 in one and 12.4 in the other would read as two different measurements.
// One decimal everywhere, so a column of them stays aligned.
func formatValue(v float64, lang string) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if lang == i18n.DefaultLang {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
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

// buildAssetCacheControl gives /static/build/ its header: every file there is
// content-hashed, so all of it is immutable.
func buildAssetCacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", immutableCacheControl)
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

// catchAll renders the 404, except that a GET/HEAD for "<page>/" gets a 301 to
// "<page>" when the slash-less path is a real page route. Subtree routes (static
// files) and the language homes are never redirected, so a redirect target is
// always a final answer and cannot loop.
func (rr *Renderer) catchAll(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if stripped := strings.TrimRight(r.URL.Path, "/"); stripped != "" && stripped != r.URL.Path {
				probe := r.Clone(r.Context())
				probe.URL.Path = stripped
				if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" && !strings.HasSuffix(pattern, "/") {
					target := stripped
					if r.URL.RawQuery != "" {
						target += "?" + r.URL.RawQuery
					}
					http.Redirect(w, r, target, http.StatusMovedPermanently)
					return
				}
			}
		}
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
	})
}
