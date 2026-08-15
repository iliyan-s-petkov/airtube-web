// Package web renders the server-side HTML.
//
// Server-rendered rather than an SPA shell (Phase 1 §9.1): the pages work with
// JavaScript disabled, they are crawlable, and the first paint does not wait on
// a bundle. Phase 3 hydrates islands into this same markup — the data-island
// attributes are the mount points.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// all:dist rather than dist — the plain form skips files beginning with "." or
// "_", which would exclude both the committed .keep and Vite's
// .vite/manifest.json, and the embed would then fail to compile on a clean
// checkout for a reason with no obvious connection to either.
//
//go:embed all:dist
var distFS embed.FS

type Renderer struct {
	cat             *i18n.Catalogue
	holder          *snapshot.Holder
	baseURL         string
	basemapStyleURL string
	frontend        config.Frontend
	defaultMetric   string
	defaultPeriod   string
	assets          Assets

	// One parsed template set per page, each cloned from the base. A single
	// set would not work: every page defines "main", and the last parse would
	// win for all of them.
	pages map[string]*template.Template
}

// NewRenderer builds the page renderer. basemapStyleURL is the MapLibre style
// JSON URL with its {key} placeholder already substituted by config.Load —
// empty means no basemap vendor is configured, and every page renders its
// data markers over a plain background instead. It is carried straight
// through to PageData rather than re-derived, so the one place a vendor
// switch happens is the config load that also derived the CSP's basemap host.
//
// cfg supplies the frontend paint values and zoom thresholds (config.Frontend)
// and the default metric/period (config.Series) that reach the browser as
// data-* attributes — see PageData. Taking the whole resolved config.Config
// rather than a growing list of scalars matches server.Options: adding a knob
// changes no signature here either.
func NewRenderer(cat *i18n.Catalogue, holder *snapshot.Holder, cfg config.Config) (*Renderer, error) {
	// config.Config.Validate rejects an empty series.periods list before
	// LoadFile ever returns one, so this cannot happen with the config this
	// package actually gets called with today. Guarded anyway: this function
	// already returns an error, and relying on a guarantee enforced by a
	// different package for an indexing operation is exactly the kind of
	// invariant that survives a refactor of validate.go silently until this
	// panics in production.
	if len(cfg.Series.PeriodNames) == 0 {
		return nil, fmt.Errorf("web: config.Series.PeriodNames is empty")
	}
	rr := &Renderer{
		cat: cat, holder: holder,
		baseURL:         strings.TrimSuffix(cfg.Listen.BaseURL, "/"),
		basemapStyleURL: cfg.Basemap.StyleURL,
		frontend:        cfg.Frontend,
		defaultMetric:   cfg.Series.DefaultMetric,
		defaultPeriod:   cfg.Series.PeriodNames[0],
		pages:           make(map[string]*template.Template),
	}
	// Parsed once at construction, like the templates: with no manifest this
	// resolves to the zero Assets, and every template call site degrades to
	// no <script> tag rather than failing.
	rr.assets, _ = LoadAssets()

	for _, page := range []string{"index", "area", "error"} {
		t, err := template.New("base.gohtml").ParseFS(templateFS,
			"templates/base.gohtml", "templates/"+page+".gohtml")
		if err != nil {
			// Parsed at startup, not per request: a template typo must fail the
			// process at boot, not produce a 500 the first time a user hits
			// that page.
			return nil, fmt.Errorf("web: parsing %s: %w", page, err)
		}
		rr.pages[page] = t
	}
	return rr, nil
}

// PageData is what every template sees. Methods rather than precomputed fields
// where the value depends on the template's own argument (T, Path).
type PageData struct {
	Lang        string
	OtherLang   string
	RequestPath string // language-stripped, e.g. "/area/sofia"
	BaseURL     string
	GeneratedAt time.Time

	Areas []AreaRow
	Area  *AreaRow

	TitleKey string
	BodyKey  string

	// Assets resolves to hashed script/style paths when a Vite build has been
	// embedded, and to nothing when the dist tree holds only .keep — see
	// assets.go and internal/web/dist/.keep.
	Assets Assets

	// BasemapStyleURL is the MapLibre style JSON URL, key already substituted,
	// or empty when no basemap vendor is configured. See config.Config.BasemapStyleURL.
	BasemapStyleURL string

	// Frontend paint values and zoom thresholds. They reach the browser as
	// data-* attributes because the CSP has no 'unsafe-inline' — there is no
	// inline <script> to put a config object in, and there never will be.
	NoDataColour       string
	MarkerStrokeColour string
	EmptyBasemapColour string
	ChartLineColour    string
	ZoomCity           int
	ZoomSensor         int
	DefaultMetric      string
	DefaultPeriod      string
	// The national fallback view the home page's map opens on. Templated, not
	// written into index.gohtml: the same three numbers are what
	// /api/v1/locate returns, and a template literal is a second home for
	// them that no test compares against the first.
	DefaultZoom int
	DefaultLon  float64
	DefaultLat  float64

	cat *i18n.Catalogue
}

type AreaRow struct {
	Slug        string
	Name        string
	Kind        string
	Lon, Lat    float64
	Zoom        int
	Covered     bool
	SensorCount int
}

type alternate struct {
	Lang string
	URL  string
}

func (p PageData) T(key string) string { return p.cat.T(p.Lang, key) }

// Path prefixes an in-site path with the current language, so every link in a
// template stays in the language the reader chose. A template that hardcoded
// "/area/…" would silently drop an English reader back to Bulgarian.
func (p PageData) Path(path string) string {
	if p.Lang == i18n.DefaultLang {
		return path
	}
	if path == "/" {
		return "/" + p.Lang + "/"
	}
	return "/" + p.Lang + path
}

func (p PageData) CanonicalURL() string { return p.BaseURL + p.Path(p.RequestPath) }

func (p PageData) OtherLangURL() string {
	other := PageData{Lang: p.OtherLang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
	return other.BaseURL + other.Path(p.RequestPath)
}

func (p PageData) Alternates() []alternate {
	out := make([]alternate, 0, len(i18n.Languages))
	for _, lang := range i18n.Languages {
		other := PageData{Lang: lang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
		out = append(out, alternate{Lang: lang, URL: other.BaseURL + other.Path(p.RequestPath)})
	}
	return out
}

func (p PageData) GeneratedAtISO() string { return p.GeneratedAt.UTC().Format(time.RFC3339) }

func (p PageData) GeneratedAtHuman() string {
	return p.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC")
}

// newPageData builds the common fields for one request.
func (rr *Renderer) newPageData(lang, path string, generatedAt time.Time) PageData {
	other := "en"
	if lang == "en" {
		other = "bg"
	}
	return PageData{
		Lang: lang, OtherLang: other, RequestPath: path,
		BaseURL: rr.baseURL, GeneratedAt: generatedAt, cat: rr.cat,
		Assets:          rr.assets,
		BasemapStyleURL: rr.basemapStyleURL,

		NoDataColour:       rr.frontend.NoDataColour,
		MarkerStrokeColour: rr.frontend.MarkerStrokeColour,
		EmptyBasemapColour: rr.frontend.EmptyBasemapColour,
		ChartLineColour:    rr.frontend.ChartLineColour,
		ZoomCity:           rr.frontend.ZoomCity,
		ZoomSensor:         rr.frontend.ZoomSensor,
		DefaultMetric:      rr.defaultMetric,
		DefaultPeriod:      rr.defaultPeriod,
		DefaultZoom:        rr.frontend.DefaultZoom,
		DefaultLon:         rr.frontend.DefaultLon,
		DefaultLat:         rr.frontend.DefaultLat,
	}
}

// pageCacheControl is what a SUCCESSFUL page render carries. 150 s matches the
// API's dataMaxAge — half the poll interval — for the same reason: a copy cached
// just after a rebuild would otherwise survive until just after the next one.
//
// A page is entity-keyed at /{lang}/area/{slug} and still public, unlike the
// entity-keyed JSON endpoints. That is safe for two specific reasons, and it
// stops being safe if either changes: the page exposes nothing beyond what the
// already-public /api/v1/areas aggregate carries — no sensor coordinates, no
// per-sensor detail — and it never calls ObserveArea, so an edge cache serving
// it cannot hide an observation the breadth counter was relying on. If this page
// ever grows sensor-level data, or starts feeding the breadth counter, it must
// become private like /api/v1/area/{slug}/sensors.
const pageCacheControl = "public, max-age=150"

// render executes one page.
//
// Rendered into a buffer first, then copied out. Writing straight to the
// ResponseWriter means a template error halfway through leaves a truncated page
// under a 200 that has already been committed — the client sees a broken page
// and the status says everything is fine.
func (rr *Renderer) render(w http.ResponseWriter, status int, page string, data PageData) {
	t, ok := rr.pages[page]
	if !ok {
		rr.writePlain(w, http.StatusInternalServerError)
		return
	}

	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		// Do not fall back to rendering the error page through the same broken
		// machinery; emit fixed plain text instead.
		rr.writePlain(w, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Cacheability is decided HERE, from the status, rather than trusted from
	// whatever the caller left in the header.
	//
	// It used to be an unconditional "public, max-age=150" set at this point,
	// which silently overwrote the "no-store" RenderError had already set one
	// call frame up — so rendered 404 and 503 pages were edge-cacheable for 150
	// seconds. The 503 is the damaging one: a transient no-snapshot window (a
	// restart, a failed poll) got pinned at the edge and served to every visitor
	// for 150 s after the process was healthy again, turning a blip into an
	// outage.
	//
	// Deriving it from the status rather than fixing the call order is
	// deliberate: ordering is a convention a future caller can break silently,
	// while an error status simply cannot be marked cacheable from here.
	if status == http.StatusOK {
		w.Header().Set("Cache-Control", pageCacheControl)
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Vary", "Accept-Encoding")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

func (rr *Renderer) writePlain(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Internal server error.\n"))
}

// RenderError renders the error page in the request's language.
//
// kind is "not_found", "unavailable" or "internal" — a fixed set, so the keys
// it builds always exist in the catalogue.
func (rr *Renderer) RenderError(w http.ResponseWriter, r *http.Request, status int, kind string) {
	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, time.Time{})
	data.TitleKey = "error." + kind + ".title"
	data.BodyKey = "error." + kind + ".body"
	// No Cache-Control set here: render derives it from the status, so an error
	// page is no-store by construction. Setting it here as well was how the
	// overwrite bug hid — it looked handled at this level and was undone below.
	rr.render(w, status, "error", data)
}
