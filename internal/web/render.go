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
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"airbg.org/internal/api"
	"airbg.org/internal/config"
	"airbg.org/internal/httpx"
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
	social          config.Social
	// File order, which is the order the switcher offers them in and the order
	// the config author chose — alphabetical would put "1y" first.
	periodNames []string
	assets      Assets
	static      StaticAssets

	// The policy the embed route sends instead of the process one, which
	// refuses all framing. See httpx.EmbedCSP.
	embedCSP string

	// One parsed template set per page, each cloned from the base. A single
	// set would not work: every page defines "main", and the last parse would
	// win for all of them.
	pages map[string]*template.Template

	// Cached /sitemap.xml, keyed on the snapshot's GeneratedAt.
	sitemapMu   sync.Mutex
	sitemapAt   time.Time
	sitemapBody []byte

	// sofiaLoc is the zone every area sentence's {time} is printed in
	// (Europe/Sofia), loaded once at construction. Falls back to UTC if the
	// zoneinfo database is somehow unavailable despite the time/tzdata
	// import in cmd/airbg — a wrong zone is a display bug, not a reason to
	// fail startup.
	sofiaLoc *time.Location
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
		social:          cfg.Social,
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

	loc, err := time.LoadLocation("Europe/Sofia")
	if err != nil {
		loc = time.UTC
	}
	rr.sofiaLoc = loc

	rr.embedCSP = httpx.EmbedCSP(cfg.Listen.CSP)

	// "embed" is parsed with base.gohtml like the rest, and then redefines
	// "base" itself: it needs base's map partials but none of its chrome.
	for _, page := range []string{"index", "area", "about", "about_project", "privacy", "licences", "error", "embed"} {
		t, err := template.New("base.gohtml").Funcs(templateFuncs).ParseFS(templateFS,
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
	// Now is the wall-clock time of THIS request, distinct from GeneratedAt
	// (the snapshot's own build time): the area-page sentence's "stale"
	// state is how far Now has drifted past GeneratedAt, not a property of
	// the snapshot itself. Set once in newPageData so a test can pin both
	// independently.
	Now time.Time

	Areas []AreaRow
	Area  *AreaRow

	// Directory is the /areas province-by-province link tree (§3 of the
	// SEO6 plan). Set only for the /areas route; nil everywhere else, which
	// is what keeps it off the home page's lighter render.
	Directory []DirGroup

	// AreaCrumbs is the breadcrumb chain for an area page: Map, then each
	// ancestor root-first, then the current area last with no URL. Empty for
	// every other page.
	AreaCrumbs []Crumb
	// AreaParentBlock, AreaChildrenBlock and AreaNearestBlock are the
	// nav.area-links blocks below the readouts strip — see buildAreaLinks.
	// Each nil when that block has nothing to show.
	AreaParentBlock   *AreaLinkBlock
	AreaChildrenBlock *AreaLinkBlock
	AreaNearestBlock  *AreaLinkBlock
	// AreaNowHTML and AreaDayHTML are the air-now and 24h sentences, fully
	// rendered server HTML including one <p> each — see areaNowHTML and
	// areaDayHTML. AreaDayHTML is empty when the state does not call for it
	// (stale, uncovered, no value, or no DayRange).
	AreaNowHTML template.HTML
	AreaDayHTML template.HTML

	TitleKey string
	BodyKey  string

	// Title and Description are the rendered <title> and meta description,
	// composed per handler from the seo.* catalogue keys (OpenProject #605).
	// Title already carries the brand suffix when it fits; see composeTitle.
	Title       string
	Description string

	// NoIndex marks a page that must not be indexed or linked as canonical: an
	// error render. base.gohtml drops canonical/alternate/OG tags and emits
	// robots noindex instead.
	NoIndex bool

	// JSONLD is the schema.org graph for this page (jsonld.go); empty emits no script.
	JSONLD template.JS

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
	ChartSeriesColours string
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
	// carries one: the catalogue is the metric vocabulary itself, so it names a
	// unit for every entry in upstream.CanonicalMetrics, whereas a scale exists
	// only where someone published a table to draw.
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
	// PeriodShortLabels are the same periods in the phone control's short form.
	PeriodShortLabels []string

	// PanelHostClass is an extra class on the sensor card's host div, beside
	// the kit's own "place-host": "embed__panel" caps the card inside the
	// embed's frame, and "place-host--docked" hides it from 1024px on the home
	// page, where the map's panel carries it (web/src/lib/panelhost.js). The one
	// real difference between the three pages' otherwise identical host
	// markup (see the "sensorCardHost" partial), so it travels as data
	// rather than a second copy of the block.
	PanelHostClass string

	// StorageKeys is the privacy section's rendered allow-list — every
	// browser-storage key this site writes, each with its plain-language
	// purpose from the catalogue. Built from static/storage-keys.json (the
	// same file the vitest/e2e guards check code and the browser against), so
	// the published list cannot drift from the enforced one — see
	// storageKeyInfos in storagekeys.go.
	StorageKeys []StorageKeyInfo
	// StorageKeysCSV is the same keys, comma-joined, for the "clear my
	// settings" island's data-keys attribute — see clearsettings.js.
	StorageKeysCSV string

	// FooterAreas are the footer's Explore links: the busiest cities in the
	// snapshot, named in the page language. Empty without a snapshot.
	FooterAreas []FooterArea
	// FacebookURL and LinkedInURL are the optional footer profiles; empty hides the icon.
	FacebookURL, LinkedInURL string

	cat *i18n.Catalogue
	// sofiaLoc is the zone the area sentences' {time} placeholders are
	// printed in — see Renderer.sofiaLoc.
	sofiaLoc *time.Location
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
	// there is no reading, or for a metric api.Scales has no band table for.
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
	// The attribution the snapshot published. Exactly one is ever set.
	Source   string
	BySource map[string]snapshot.SourceEntry
	// ParentSlug mirrors snapshot.AreaMeta.ParentSlug — see ParentChain.
	ParentSlug string
	// Day is the 24h min/max range, or nil — see snapshot.DayRange and
	// PageData.AreaDay.
	Day *snapshot.DayRange
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
	// Gauge draws the figure inside an arc of Percent, filled in Colour — the
	// two µg/m³ cells, where a number alone says nothing about how bad it is.
	// A count has no scale to be a fraction of, so it stays a plain figure.
	Gauge   bool
	Percent int
	Colour  string
}

// readoutMetricSep joins a metric name to the rest of a Readout's Label (see
// the "· " built at Label: metric + " · " + ... above).
const readoutMetricSep = " · "

// templateFuncs are helpers available to every page template.
var templateFuncs = template.FuncMap{
	// readoutMetric and readoutRest split a Readout's Label back into its
	// metric prefix and the rest, so the template can wrap the prefix in its
	// own span without a new field on Readout or a second copy of the i18n
	// string that builds Label.
	"readoutMetric": func(label string) string {
		if before, _, ok := strings.Cut(label, readoutMetricSep); ok {
			return before
		}
		return ""
	},
	"readoutRest": func(label string) string {
		if _, after, ok := strings.Cut(label, readoutMetricSep); ok {
			return after
		}
		return label
	},
}

// gauge turns the cell into an arc, or leaves it a plain figure when the metric
// publishes no scale to be a fraction of.
func (r *Readout) gauge(metric string, value float64) {
	pct, ok := gaugePercent(metric, value)
	if !ok {
		return
	}
	r.Gauge, r.Percent, r.Colour = true, pct, bandColour(metric, value)
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
	// Both figures are of ONE metric — the map's default — and the card used to
	// name only the unit, so "113,5 µg/m³" left the reader to guess whether it
	// was PM2.5 or PM10.
	metric := p.T("metric." + p.DefaultMetric)
	highest := Readout{Label: metric + " · " + p.T("read.highest"), Value: none, Tier: p.T("home.tier_silent")}
	median := Readout{Label: metric + " · " + p.T("read.median"), Value: none}
	if len(values) > 0 {
		medianValue := medianOf(values)
		highest.Value, highest.Unit = formatValue(topValue, p.Lang), unit
		highest.Tier = top + " · " + p.T("areas.tier")
		median.Value, median.Unit = formatValue(medianValue, p.Lang), unit
		highest.gauge(p.DefaultMetric, topValue)
		median.gauge(p.DefaultMetric, medianValue)
	}
	median.Tier = strconv.Itoa(len(values)) + " " + p.T("home.tier_covered")

	// The counts carry no unit: their labels already say what was counted, and
	// "608 сензора" under "Сензори в мрежата" prints the word twice.
	return []Readout{
		highest,
		median,
		{Label: p.T("read.sensors"), Value: strconv.Itoa(sensors), Tier: p.T("home.tier_sensors")},
		silentReadout(p.T("read.no_data"), silent, len(p.Areas), p.NoDataColour, p.T("read.of_total")),
	}
}

// StripReadouts is whichever set the page in hand draws, so the one island
// wrapper in base.gohtml serves both without either template naming the other's
// field.
func (p PageData) StripReadouts() []Readout {
	if p.Area != nil {
		return p.AreaReadouts()
	}
	return p.Readouts()
}

// SensorReadoutRow reports whether the readouts island should also mount its
// own client-side row for the open sensor. False on an area page: its strip
// already states the area's medians and the sensor card already states the
// open sensor's readings, so the row would restate both. True everywhere else
// the readouts-island partial appears, including the index page, where the
// strip is national rather than area-scoped and the row is additive.
func (p PageData) SensorReadoutRow() bool {
	return p.Area == nil
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
		c := Readout{
			Label: p.T("metric." + m),
			Value: formatValue(v, p.Lang),
			Unit:  p.T("unit." + m),
			Tier:  tier,
		}
		c.gauge(m, v)
		out = append(out, c)
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
	out = append(out, Readout{
		Label: p.T("table.col.sensors"),
		Value: strconv.Itoa(p.Area.SensorCount),
		Tier:  p.T("area.tier_sensors"),
	})
	return out
}

// silentReadout draws the silent count as a share of every province there is —
// a count with a known total is a fraction, and 9 of 28 says something 9 alone
// does not. Painted in the map's own no-data colour so the ring and the grey
// provinces under it are visibly the same statement.
func silentReadout(label string, silent, total int, colour, tier string) Readout {
	r := Readout{Label: label, Value: strconv.Itoa(silent), Tier: tier}
	if total <= 0 || colour == "" {
		return r
	}
	// With a total to state, the tier line states it. "No recent readings" only
	// repeated the card's own label in other words; "of 28 provinces in all" is
	// the denominator the ring is drawn against, which is the one thing the
	// figure alone cannot say.
	r.Tier = strings.ReplaceAll(tier, "{total}", strconv.Itoa(total))
	pct := int(math.Round(float64(silent) / float64(total) * 100))
	r.Gauge, r.Percent, r.Colour = true, pct, colour
	return r
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

// flagURL returns the content-stamped URL of a language's flag, or "" when the
// checkout ships none for it. Stamped so it is served immutable like app.css.
func (p PageData) flagURL(lang string) string {
	name := "flags/" + lang + ".svg"
	if _, err := staticFS.Open("static/" + name); err != nil {
		return ""
	}
	return p.static.URL(name)
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

// InitialTierKey is the caption tier the map island settles on at the opening zoom.
// Never "sensors": cellTier (mapdata.js) only says so above the grid's point handover, which no page opens at.
func (p PageData) InitialTierKey() string {
	zoom := p.DefaultZoom
	if p.Area != nil {
		zoom = p.Area.Zoom
	}
	if zoom < p.ZoomCity {
		return "country"
	}
	return "city"
}

// InitialTier is the server-rendered legend__tier text; mountChrome adopts the node.
func (p PageData) InitialTier() string {
	return p.T("map.legend.tier." + p.InitialTierKey())
}

// titleBrandSep joins a title core to the brand name — see composeTitle.
const titleBrandSep = " | "

// composeTitle appends " | <brand>" to core, but only when the result stays
// within Google's ~60-character display budget; past it the suffix is
// dropped rather than truncating the core, which would cut off the place
// name a search result is trying to match (OpenProject #605 / seo-copy.md §2).
func composeTitle(core, brand string) string {
	full := core + titleBrandSep + brand
	if utf8.RuneCountInString(full) <= 60 {
		return full
	}
	return core
}

// MetaDescription is the meta/og description: the per-page value a handler
// set, or the site tagline for a page that set none (today, only the error
// page).
func (p PageData) MetaDescription() string {
	if p.Description != "" {
		return p.Description
	}
	return p.T("site.tagline")
}

// OGTitle is the title core with the brand suffix stripped, per seo-copy.md
// §5: og:site_name already carries the brand, so og:title repeating it would
// be redundant on every share card.
func (p PageData) OGTitle() string {
	return strings.TrimSuffix(p.Title, titleBrandSep+p.T("seo.title_brand"))
}

// BaseHost is the host of BaseURL, or BaseURL itself when it does not parse.
func (p PageData) BaseHost() string {
	if u, err := url.Parse(p.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return p.BaseURL
}

// OGImageURL is the absolute URL of the static share-card image, built from
// BaseURL rather than hard-coded, so it agrees with whatever host the
// deployment is actually configured for.
func (p PageData) OGImageURL() string { return p.BaseURL + p.Static("social-preview.png") }

// OGLocale is this page's og:locale, in the seo.locale key's og-style form
// ("bg_BG", not "bg").
func (p PageData) OGLocale() string { return p.T("seo.locale") }

// OGLocaleAlternates is og:locale:alternate for every OTHER served language,
// mirroring LangLinks/Alternates: the set is whatever catalogues loaded, not a
// hardcoded pair.
func (p PageData) OGLocaleAlternates() []string {
	langs := p.cat.Languages()
	out := make([]string, 0, len(langs))
	for _, lang := range langs {
		if lang == p.Lang {
			continue
		}
		out = append(out, p.cat.T(lang, "seo.locale"))
	}
	return out
}

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

// PeriodShortLabelsAttr is the phone segmented control's labels (24h, 7d).
func (p PageData) PeriodShortLabelsAttr() string { return strings.Join(p.PeriodShortLabels, ",") }

// areaMeasuredMetrics is the metric keys the chart offers for this one area:
// the default metric first if this area measures it, then whichever of the
// rest this area actually reports, in canonical order — the same rule
// AreaReadouts cells by, including the default: a menu entry that cannot
// plot is worse than one fewer entry. Kept separate from
// Metrics/MetricLabels/MetricUnits above, which are the site's whole
// vocabulary (what the top switcher and the map offer) — a metric this area
// has no sensor for still belongs on those, but a menu entry for it on the
// chart would ask the API for a series it can never return.
//
// Nil for an area with no measured metric at all: the chart still mounts
// (data-metric keeps naming the site default, for the heading and the
// fetch), but with no menu to offer, and a fetch for an unmeasured metric
// resolves through the chart's own existing failed-fetch path to its
// unavailable message — no second "no metrics" string is needed.
func (p PageData) areaMeasuredMetrics() []string {
	if p.Area == nil {
		return nil
	}
	out := make([]string, 0, len(p.Metrics))
	if _, ok := p.Area.Values[p.DefaultMetric]; ok {
		out = append(out, p.DefaultMetric)
	}
	for _, m := range p.Metrics {
		if m == p.DefaultMetric {
			continue
		}
		if _, ok := p.Area.Values[m]; ok {
			out = append(out, m)
		}
	}
	return out
}

// AreaMetricsAttr, AreaMetricLabelsAttr and AreaMetricUnitsAttr are the
// area-scoped counterpart to MetricsAttr/MetricLabelsAttr/MetricUnitsAttr,
// comma-joined the same way. The chart island's own metric menu reads these,
// not the global lists.
func (p PageData) AreaMetricsAttr() string { return strings.Join(p.areaMeasuredMetrics(), ",") }

func (p PageData) AreaMetricLabelsAttr() string {
	metrics := p.areaMeasuredMetrics()
	labels := make([]string, len(metrics))
	for i, m := range metrics {
		labels[i] = p.T("metric." + m)
	}
	return strings.Join(labels, ",")
}

func (p PageData) AreaMetricUnitsAttr() string {
	metrics := p.areaMeasuredMetrics()
	units := make([]string, len(metrics))
	for i, m := range metrics {
		units[i] = p.T("unit." + m)
	}
	return strings.Join(units, ",")
}

// AreaTier is the wording for what an aggregate on this page covers — a
// province or a city. The chart's heading is composed in the browser from the
// metric, the period and this, so it has to arrive as its own string; the
// readouts strip uses the same two keys for the same reason.
func (p PageData) AreaTier() string {
	if p.Area != nil {
		switch p.Area.Kind {
		case "city":
			return p.T("area.tier_city")
		case "neighbourhood":
			return p.T("area.tier_district")
		}
	}
	return p.T("areas.tier")
}

// HasBasemap reports whether the page renders basemap tiles, which is what
// makes the footer's ODbL credit required — and, when false, wrong.
func (p PageData) HasBasemap() bool { return p.BasemapStyleURL != "" }

// AttributionURL looks up the licence URL api.Attributions() publishes for a
// source, so the footer's links cannot drift from what /api/v1/meta reports.
// It panics on an unknown source: html/template recovers a panicking template
// function into an execution error, which is safer for a licence-required
// link than silently rendering a dead href.
func (p PageData) AttributionURL(source string) string {
	for _, a := range api.Attributions() {
		if a.Source == source {
			return a.URL
		}
	}
	panic(fmt.Sprintf("web: no attribution for source %q", source))
}

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
			Flag:    p.flagURL(lang),
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

// XDefaultURL is the default-language alternate, which the head names x-default
// as the sitemap does. Empty when no served language is the default.
func (p PageData) XDefaultURL() string {
	for _, a := range p.Alternates() {
		if a.Lang == i18n.DefaultLang {
			return a.URL
		}
	}
	return ""
}

func (p PageData) GeneratedAtISO() string { return p.GeneratedAt.UTC().Format(time.RFC3339) }

func (p PageData) GeneratedAtHuman() string {
	return p.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC")
}

// Static is the URL for a hand-written static file, carrying the hash of what
// is currently embedded, so an edit cannot be served from a stale cache.
func (p PageData) Static(name string) string { return p.static.URL(name) }

// FooterArea is one city link in the footer.
type FooterArea struct{ Slug, Name string }

// footerCityCount is how many cities the footer's Explore column lists.
const footerCityCount = 4

// footerAreas picks the cities with the most sensors from the snapshot, so the
// slugs are the ones the area pages route on. Ties break by slug for a stable page.
func (rr *Renderer) footerAreas(lang string) []FooterArea {
	snap := rr.holder.Load()
	if snap == nil {
		return nil
	}
	cities := make([]snapshot.AreaMeta, 0, len(snap.KnownSlugs))
	for _, meta := range snap.KnownSlugs {
		if meta.Kind == "city" {
			cities = append(cities, meta)
		}
	}
	sort.Slice(cities, func(i, j int) bool {
		if cities[i].SensorCount != cities[j].SensorCount {
			return cities[i].SensorCount > cities[j].SensorCount
		}
		return cities[i].Slug < cities[j].Slug
	})
	if len(cities) > footerCityCount {
		cities = cities[:footerCityCount]
	}
	out := make([]FooterArea, len(cities))
	for i, meta := range cities {
		out[i] = FooterArea{Slug: meta.Slug, Name: rr.rowFrom(meta, lang).Name}
	}
	return out
}

// Year is the current year for the footer's copyright line.
func (p PageData) Year() int { return p.Now.Year() }

// SourceIssuesURL is where the footer's Contact link goes until an email is chosen.
func (p PageData) SourceIssuesURL() string { return sourceRepoURL + "/issues" }

// SourceLicenceURL is the repository's LICENSE file.
func (p PageData) SourceLicenceURL() string { return sourceRepoURL + "/blob/master/LICENSE" }

// SourceRepoURL is the public repository; templates take it from here so the
// address is written once (jsonld.go).
func (p PageData) SourceRepoURL() string { return sourceRepoURL }

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
	periodShort := make([]string, len(rr.periodNames))
	for i, p := range rr.periodNames {
		periodLabels[i] = rr.cat.T(lang, "period."+p)
		periodShort[i] = rr.cat.T(lang, "period.short."+p)
	}
	return PageData{
		Lang: lang, RequestPath: path,
		BaseURL: rr.baseURL, GeneratedAt: generatedAt, Now: time.Now(),
		cat: rr.cat, sofiaLoc: rr.sofiaLoc,
		Assets:          rr.assets,
		static:          rr.static,
		BasemapStyleURL: rr.basemapStyleURL,
		FooterAreas:     rr.footerAreas(lang),
		FacebookURL:     rr.social.FacebookURL,
		LinkedInURL:     rr.social.LinkedInURL,

		NoDataColour:       rr.frontend.NoDataColour,
		UnscaledColour:     rr.frontend.UnscaledColour,
		MarkerStrokeColour: rr.frontend.MarkerStrokeColour,
		MarkerLabelColour:  rr.frontend.MarkerLabelColour,
		EmptyBasemapColour: rr.frontend.EmptyBasemapColour,
		HexOpacity:         rr.frontend.HexOpacity,
		ChartLineColour:    rr.frontend.ChartLineColour,
		ChartCompareColour: rr.frontend.ChartCompareColour,
		ChartSeriesColours: rr.frontend.ChartSeriesColours,
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
		PeriodShortLabels:  periodShort,
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
	data.Title = data.T(data.TitleKey) + titleBrandSep + data.T("seo.title_brand")
	// Never indexed and never a canonical target: base.gohtml drops
	// canonical/alternate/OG tags for this page and emits robots noindex
	// instead — see OpenProject #605's error-page audit finding.
	data.NoIndex = true
	// No Cache-Control set here: render derives it from the status, so an error
	// page is no-store by construction. Setting it here as well was how the
	// overwrite bug hid — it looked handled at this level and was undone below.
	rr.render(w, r, status, "error", data)
}
