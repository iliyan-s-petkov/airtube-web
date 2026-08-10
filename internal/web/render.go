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

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Renderer struct {
	cat     *i18n.Catalogue
	holder  *snapshot.Holder
	baseURL string

	// One parsed template set per page, each cloned from the base. A single
	// set would not work: every page defines "main", and the last parse would
	// win for all of them.
	pages map[string]*template.Template
}

func NewRenderer(cat *i18n.Catalogue, holder *snapshot.Holder, baseURL string) (*Renderer, error) {
	rr := &Renderer{
		cat: cat, holder: holder,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		pages:   make(map[string]*template.Template),
	}

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

func (p PageData) GeneratedAtHuman() string { return p.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC") }

// newPageData builds the common fields for one request.
func (rr *Renderer) newPageData(lang, path string, generatedAt time.Time) PageData {
	other := "en"
	if lang == "en" {
		other = "bg"
	}
	return PageData{
		Lang: lang, OtherLang: other, RequestPath: path,
		BaseURL: rr.baseURL, GeneratedAt: generatedAt, cat: rr.cat,
	}
}

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
	w.Header().Set("Cache-Control", "public, max-age=150")
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
	w.Header().Set("Cache-Control", "no-store")
	rr.render(w, status, "error", data)
}
