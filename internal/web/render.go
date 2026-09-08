// Package web renders the server-side HTML.
//
// Server-rendered rather than an SPA shell (Phase 1 §9.1): the pages work with
// JavaScript disabled, they are crawlable, and the first paint does not wait on
// a bundle. Phase 3 hydrates islands into this same markup — the data-island
// attributes are the mount points.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
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
	// File order, which is the order the switcher offers them in and the order
	// the config author chose — alphabetical would put "1y" first.
	periodNames []string
	assets      Assets
	static      StaticAssets

	// One parsed template set per page, each cloned from the base. A single
	// set would not work: every page defines "main", and the last parse would
	// win for all of them.
	pages map[string]*template.Template
}

// NewRenderer builds the page renderer.
//
// The basemap style URL is derived from config.Tiles, which is empty when no
// basemap is configured — the map then renders data markers over a plain
// background instead. Derived once, here, because the same tiles.public_url
// also produces the CSP origin the browser must be allowed to fetch from, and
// two copies is how those two drift apart.
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
		basemapStyleURL: cfg.Tiles.StyleURL(),
		frontend:        cfg.Frontend,
		defaultMetric:   cfg.Series.DefaultMetric,
		defaultPeriod:   cfg.Series.PeriodNames[0],
		periodNames:     cfg.Series.PeriodNames,
		pages:           make(map[string]*template.Template),
	}
	// Parsed once at construction, like the templates: with no manifest this
	// resolves to the zero Assets, and every template call site degrades to
	// no <script> tag rather than failing.
	rr.assets, _ = LoadAssets()
	rr.static = LoadStaticAssets()

	for _, page := range []string{"index", "area", "about", "error"} {
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

	// static stamps the hand-written /static/ files with their content hash;
	// templates reach it through the Static method below.
	static StaticAssets

	// BasemapStyleURL is the self-hosted MapLibre style document's URL, or
	// empty when no basemap is configured. See config.Tiles.StyleURL.
	BasemapStyleURL string

	// Frontend paint values and zoom thresholds. They reach the browser as
	// data-* attributes because the CSP has no 'unsafe-inline' — there is no
	// inline <script> to put a config object in, and there never will be.
	NoDataColour       string
	UnscaledColour     string
	MarkerStrokeColour string
	MarkerLabelColour  string
	EmptyBasemapColour string
	HexOpacity         float64
	ChartLineColour    string
	ChartCompareColour string
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

	// Metrics, MetricLabels and MetricUnits are POSITIONAL triples:
	// MetricLabels[i] names Metrics[i] and MetricUnits[i] is what it is
	// measured in. Parallel attributes rather than a JSON blob because the CSP
	// has no 'unsafe-inline' and data-* attributes are the only channel.
	//
	// The units come from the catalogue and not from /api/v1/scales, which also
	// carries one: that endpoint has an entry only for a metric with a band
	// table, which today is two of the seven. The legend has to name a unit for
	// all seven.
	Metrics      []string
	MetricLabels []string
	MetricUnits  []string

	// Periods and PeriodLabels are the same positional pairing for the chart's
	// window switcher, in the config's file order.
	//
	// Offered from the server's own vocabulary rather than listed in a template
	// or an island: the API rejects any period it does not recognise (see
	// api.parsePeriod), so a hard-coded list is a button that starts returning
	// 400 the day someone edits series.periods. The design kit's mockup lists
	// 6/12/24/42-hour windows, which describe a 42-hour archive; this site keeps
	// a year, so the vocabulary is read rather than copied.
	Periods      []string
	PeriodLabels []string

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
	// Value is the reading for the page's default metric; HasValue says
	// whether there is one. 0 is a legitimate reading, so absence gets its own
	// flag rather than being encoded as a zero the template would print.
	Value    float64
	HasValue bool
	// Pre-formatted, because the decimal separator is the language's: Bulgarian
	// writes 12,4 where English writes 12.4, and a Go template cannot localise
	// a float on its own. Formatting once here also keeps every row identical
	// in precision.
	ValueText string
	// The band colour for Value under the page's default metric, or empty when
	// there is no reading or the metric has no band table (five of the seven).
	// Server-side because the row is server-rendered: the table's swatch has to
	// be right with no JavaScript, and it must agree with the dot the map draws
	// for the same province — see bandColour.
	Colour string
	// Every metric this area is currently reporting, unformatted. Value above is
	// one of these — the page's default metric — kept as its own field because
	// the province list only ever prints that one and reaching into a map per
	// row in a template is how a missing key becomes a silent blank cell. The
	// area page needs the rest: it is the page about this one area, so it shows
	// what the area measures rather than the one column a list can hold.
	Values map[string]float64
}

// Readout is one cell of the country summary strip: what was measured, the
// figure, its unit, and one line saying what the figure covers. Tier is part
// of the cell rather than decoration — a bare number on this page would not
// say whether it is one sensor, one province, or the country (DESIGN.md §9.1).
type Readout struct {
	Label string
	Value string
	Unit  string
	Tier  string
}

// Readouts summarises the province list the page already renders rather than
// asking the snapshot a second set of questions. The strip and the list are
// then two views of one set of numbers and cannot drift apart — a "highest"
// cell that named a province the list below ranked second would be worse than
// no cell at all.
//
// Nil when there is no list, so a page without one (about, error) renders no
// strip instead of four zeroes.
func (p PageData) Readouts() []Readout {
	if len(p.Areas) == 0 {
		return nil
	}

	values := make([]float64, 0, len(p.Areas))
	top, topValue := "", 0.0
	sensors, silent := 0, 0
	for _, a := range p.Areas {
		// Sensor counts come from covered provinces only: an uncovered one
		// reports a count the aggregates do not use, and adding it here would
		// make the strip's total disagree with what the map is drawing.
		if a.Covered {
			sensors += a.SensorCount
		}
		if !a.HasValue {
			silent++
			continue
		}
		if len(values) == 0 || a.Value > topValue {
			topValue, top = a.Value, a.Name
		}
		values = append(values, a.Value)
	}

	unit := p.T("unit." + p.DefaultMetric)
	none := p.T("panel.no_value")
	// The tier line is not optional. With nothing reporting, "highest" still
	// has to say WHY there is no figure — an empty third line reads as a cell
	// that failed to render rather than a country that is quiet tonight.
	highest := Readout{Label: p.T("read.highest"), Value: none, Tier: p.T("home.tier_silent")}
	median := Readout{Label: p.T("read.median"), Value: none}
	if len(values) > 0 {
		highest.Value, highest.Unit = formatValue(topValue, p.Lang), unit
		highest.Tier = top + " · " + p.T("areas.tier")
		median.Value, median.Unit = formatValue(medianOf(values), p.Lang), unit
	}
	median.Tier = strconv.Itoa(len(values)) + " " + p.T("home.tier_covered")

	return []Readout{
		highest,
		median,
		{Label: p.T("read.sensors"), Value: strconv.Itoa(sensors), Tier: p.T("home.tier_sensors")},
		{Label: p.T("read.no_data"), Value: strconv.Itoa(silent), Tier: p.T("home.tier_silent")},
	}
}

// AreaReadouts is the strip at the top of one area's page: what this area is
// currently measuring, one cell per metric, then how many sensors the figures
// come from.
//
// One cell per metric the area actually reports, rather than a fixed four:
// which instruments an area carries is a property of the area, and a cell
// reading nothing would claim the site looked and found the air unmeasurable
// when in fact no sensor there carries that instrument. The cells follow the
// site's canonical metric order so the strip is byte-identical between two
// requests — a map's iteration order is not an order.
//
// Nil for an uncovered area. It publishes no average at all, and the page
// already says so in a sentence; a strip of cells beside that notice would
// contradict it.
func (p PageData) AreaReadouts() []Readout {
	if p.Area == nil || !p.Area.Covered {
		return nil
	}

	// The tier line is the whole point of the cell: without it the figure is a
	// number on a page about a place, and a reader cannot tell whether it is one
	// sensor's reading or the average of two hundred.
	tier := p.AreaTier()

	out := make([]Readout, 0, len(p.Metrics)+1)
	cell := func(m string) {
		v, ok := p.Area.Values[m]
		if !ok {
			return
		}
		out = append(out, Readout{
			Label: p.T("metric." + m),
			Value: formatValue(v, p.Lang),
			Unit:  p.T("unit." + m),
			Tier:  tier,
		})
	}

	// The default metric leads, then the rest in canonical order. Canonical
	// order is alphabetical, which would put PM10 in the first cell while the
	// chart, the map and the province list on the same site are all showing
	// PM2.5 — the strip would open by answering a question the page is not
	// asking.
	cell(p.DefaultMetric)
	for _, m := range p.Metrics {
		if m != p.DefaultMetric {
			cell(m)
		}
	}

	// Always last and always present, even when nothing is reporting: the count
	// is a fact about the network rather than a measurement, and on a silent
	// night it is the number that explains the silence.
	//
	// Labelled from the table's column rather than read.sensors: that key reads
	// "Sensors in the network", which is the country figure on the home page and
	// would be a false claim about one province here.
	return append(out, Readout{
		Label: p.T("table.col.sensors"),
		Value: strconv.Itoa(p.Area.SensorCount),
		Tier:  p.T("area.tier_sensors"),
	})
}

// medianOf takes ownership of values and sorts it in place. The median rather
// than the mean because a handful of provinces sitting in a temperature
// inversion pulls a national mean somewhere no province actually is.
func medianOf(values []float64) float64 {
	sort.Float64s(values)
	n := len(values)
	if n%2 == 1 {
		return values[n/2]
	}
	return (values[n/2-1] + values[n/2]) / 2
}

type alternate struct {
	Lang string
	URL  string
}

// langLink is one entry in the language switcher. Name is that language's name
// written IN that language ("Български", not "Bulgarian"): a reader who cannot
// read the current page's language is exactly the reader the switcher is for.
//
// Flag is a path or "". A flag names a nation and not every language has one, so
// the picker falls back to Code, the language's own two letters. Presence is
// decided by whether
// static/flags/<lang>.svg exists, which keeps adding a language a matter of
// dropping in files rather than editing this type.
type langLink struct {
	Lang    string
	URL     string
	Name    string
	Code    string
	Flag    string
	Current bool
}

// langFlag returns the served path of a language's flag, or "" when the
// checkout ships none for it.
func langFlag(lang string) string {
	name := "static/flags/" + lang + ".svg"
	if _, err := staticFS.Open(name); err != nil {
		return ""
	}
	return "/" + name
}

// SilentAreas counts the rows the table prints with no reading. Derived from
// the rows themselves rather than carried as a field: the count line under the
// table says how many of the rows above it are silent, and a stored number is
// how that sentence starts disagreeing with the table it describes.
func (p PageData) SilentAreas() int {
	n := 0
	for _, a := range p.Areas {
		if !a.HasValue {
			n++
		}
	}
	return n
}

func (p PageData) T(key string) string { return p.cat.T(p.Lang, key) }

// MetricsAttr and MetricLabelsAttr are the comma-joined form of Metrics and
// MetricLabels that the switcher island's data-metrics / data-metric-labels
// attributes carry. Joined here, not in the template, so the same rule that
// splits them back apart in web/src/lib/metrics.js (parseMetricList) has one
// counterpart on this side, not a {{range}} loop reproducing it.
func (p PageData) MetricsAttr() string      { return strings.Join(p.Metrics, ",") }
func (p PageData) MetricLabelsAttr() string { return strings.Join(p.MetricLabels, ",") }
func (p PageData) MetricUnitsAttr() string  { return strings.Join(p.MetricUnits, ",") }
func (p PageData) PeriodsAttr() string      { return strings.Join(p.Periods, ",") }
func (p PageData) PeriodLabelsAttr() string { return strings.Join(p.PeriodLabels, ",") }

// AreaTier is the wording for what an aggregate on this page covers — a
// province or a city. The chart's heading is composed in the browser from the
// metric, the period and this, so it has to arrive as its own string; the
// readouts strip uses the same two keys for the same reason.
func (p PageData) AreaTier() string {
	if p.Area != nil && p.Area.Kind == "city" {
		return p.T("area.tier_city")
	}
	return p.T("areas.tier")
}

// HasBasemap reports whether the page renders basemap tiles, which is what
// makes the footer's ODbL credit required — and, when false, wrong.
func (p PageData) HasBasemap() bool { return p.BasemapStyleURL != "" }

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

// OnPath reports whether the page being rendered IS the given route, so the
// masthead can mark the current tab. Compared against RequestPath, which is
// language-stripped — the English reader of /en/areas is on /areas.
func (p PageData) OnPath(path string) bool { return p.RequestPath == path }

// LangPrefix is Path's prefix on its own — "" for the default language,
// "/<lang>" otherwise — rendered into the map island as data-lang-prefix.
//
// The island needs it because the language set is data: no expression in the
// browser can tell whether the first path segment is a language or a page, so
// the client cannot derive what the server already knows. Without it, a click
// on a marker returns a non-default reader to the default language.
func (p PageData) LangPrefix() string {
	if p.Lang == i18n.DefaultLang {
		return ""
	}
	return "/" + p.Lang
}

// LangLinks is the switcher: one entry per served language, in the catalogue's
// display order, each pointing at the SAME page in that language.
//
// A list rather than the old binary "other language" link, because the served
// set is whatever catalogues loaded — dropping de.json into i18n.dir adds a
// third link here with no code change. The current language is included and
// marked rather than filtered out, so the switcher does not change width when a
// reader switches, and a template can render it as the selected item.
func (p PageData) LangLinks() []langLink {
	langs := p.cat.Languages()
	out := make([]langLink, 0, len(langs))
	for _, lang := range langs {
		other := PageData{Lang: lang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
		out = append(out, langLink{
			Lang:    lang,
			URL:     other.BaseURL + other.Path(p.RequestPath),
			Name:    p.cat.T(lang, "lang.name"),
			Code:    p.cat.T(lang, "lang.code"),
			Flag:    langFlag(lang),
			Current: lang == p.Lang,
		})
	}
	return out
}

func (p PageData) Alternates() []alternate {
	langs := p.cat.Languages()
	out := make([]alternate, 0, len(langs))
	for _, lang := range langs {
		other := PageData{Lang: lang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
		out = append(out, alternate{Lang: lang, URL: other.BaseURL + other.Path(p.RequestPath)})
	}
	return out
}

func (p PageData) GeneratedAtISO() string { return p.GeneratedAt.UTC().Format(time.RFC3339) }

func (p PageData) GeneratedAtHuman() string {
	return p.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC")
}

// Static is the URL for a hand-written static file, carrying the hash of what
// is currently embedded, so an edit cannot be served from a stale cache.
func (p PageData) Static(name string) string { return p.static.URL(name) }

// newPageData builds the common fields for one request.
func (rr *Renderer) newPageData(lang, path string, generatedAt time.Time) PageData {
	// CanonicalMetrics is sorted, not map-ordered — see its own doc comment —
	// so this order is stable across requests and processes; the switcher's
	// server test pins the exact string it produces.
	metrics := upstream.CanonicalMetrics()
	labels := make([]string, len(metrics))
	units := make([]string, len(metrics))
	for i, m := range metrics {
		labels[i] = rr.cat.T(lang, "metric."+m)
		units[i] = rr.cat.T(lang, "unit."+m)
	}
	// Same shape for the chart's periods: the vocabulary comes from the config,
	// the labels from the catalogue, and the two ride as parallel lists the
	// island reads by index.
	periodLabels := make([]string, len(rr.periodNames))
	for i, p := range rr.periodNames {
		periodLabels[i] = rr.cat.T(lang, "period."+p)
	}
	return PageData{
		Lang: lang, RequestPath: path,
		BaseURL: rr.baseURL, GeneratedAt: generatedAt, cat: rr.cat,
		Assets:          rr.assets,
		static:          rr.static,
		BasemapStyleURL: rr.basemapStyleURL,

		NoDataColour:       rr.frontend.NoDataColour,
		UnscaledColour:     rr.frontend.UnscaledColour,
		MarkerStrokeColour: rr.frontend.MarkerStrokeColour,
		MarkerLabelColour:  rr.frontend.MarkerLabelColour,
		EmptyBasemapColour: rr.frontend.EmptyBasemapColour,
		HexOpacity:         rr.frontend.HexOpacity,
		ChartLineColour:    rr.frontend.ChartLineColour,
		ChartCompareColour: rr.frontend.ChartCompareColour,
		ZoomCity:           rr.frontend.ZoomCity,
		ZoomSensor:         rr.frontend.ZoomSensor,
		DefaultMetric:      rr.defaultMetric,
		DefaultPeriod:      rr.defaultPeriod,
		DefaultZoom:        rr.frontend.DefaultZoom,
		DefaultLon:         rr.frontend.DefaultLon,
		DefaultLat:         rr.frontend.DefaultLat,
		Metrics:            metrics,
		MetricLabels:       labels,
		MetricUnits:        units,
		Periods:            rr.periodNames,
		PeriodLabels:       periodLabels,
	}
}

// pageCacheControl is what a SUCCESSFUL page render carries.
//
// max-age=0 with an ETag, not a TTL: a page names the content-hashed bundle it
// loads, so a cached page pins a whole deploy's worth of frontend. Revalidation
// is a 304 against the ETag below, which costs one render and no body.
//
// A page is entity-keyed at /{lang}/area/{slug} and still public, unlike the
// entity-keyed JSON endpoints. That is safe for two specific reasons, and it
// stops being safe if either changes: the page exposes nothing beyond what the
// already-public /api/v1/areas aggregate carries — no sensor coordinates, no
// per-sensor detail — and it never calls ObserveArea, so an edge cache serving
// it cannot hide an observation the breadth counter was relying on. If this page
// ever grows sensor-level data, or starts feeding the breadth counter, it must
// become private like /api/v1/area/{slug}/sensors.
const pageCacheControl = "public, max-age=0, must-revalidate"

// render executes one page.
//
// Rendered into a buffer first, then copied out. Writing straight to the
// ResponseWriter means a template error halfway through leaves a truncated page
// under a 200 that has already been committed — the client sees a broken page
// and the status says everything is fine.
func (rr *Renderer) render(w http.ResponseWriter, r *http.Request, status int, page string, data PageData) {
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

	body := buf.String()

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

	// The ETag is what makes max-age=0 cheap, and it is only set on a 200: an
	// error page is no-store, so a validator for it would be a cache key for a
	// response no cache may keep.
	if status == http.StatusOK {
		sum := sha256.Sum256([]byte(body))
		etag := `"` + hex.EncodeToString(sum[:])[:16] + `"`
		w.Header().Set("ETag", etag)
		if matchesETag(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// matchesETag reports whether an If-None-Match header covers etag.
//
// "*" matches anything, and the header may carry a list; a weak validator
// ("W/...") compares equal to its strong form, which is what a proxy that
// weakened the tag on the way out will send back.
func matchesETag(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
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
	lang, path := rr.cat.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, time.Time{})
	data.TitleKey = "error." + kind + ".title"
	data.BodyKey = "error." + kind + ".body"
	// No Cache-Control set here: render derives it from the status, so an error
	// page is no-store by construction. Setting it here as well was how the
	// overwrite bug hid — it looked handled at this level and was undone below.
	rr.render(w, r, status, "error", data)
}
