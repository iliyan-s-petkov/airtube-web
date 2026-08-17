// Package i18n serves the UI message catalogues.
//
// Bulgarian is the default and every other language lives under its own path
// prefix — /en/, /de/ (Phase 1 §9.5). Bulgarian and English are embedded, so a
// missing file is a build error rather than a deployment that starts fine and
// renders blank labels.
//
// A language is DATA, not code: the set is whatever catalogues are present at
// startup — the embedded ones plus any <lang>.json in the operator's override
// directory. Adding a language is adding a file. Nothing in this package, in
// routing, or in the templates enumerates "bg" and "en".
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed *.json
var catalogueFS embed.FS

// DefaultLang is served at the site root, with no path prefix. It is also the
// completeness reference: every other catalogue must translate its keys.
const DefaultLang = "bg"

// langPattern is what a catalogue filename may name. A language code becomes a
// URL path segment ("/de/area/sofia") and an HTML lang attribute, so the set of
// characters allowed here is deliberately tiny: an operator dropping a file
// into the override directory must not be able to invent a path segment.
var langPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]{2})?$`)

// areaNamePrefix marks the keys whose names are not known at build time: one
// per area slug, so a translator can render "София" as "Sofia" or "Sofija"
// without a database column per language (see internal/web.rowFrom). They are
// the one exception to the "every key must exist in the default catalogue"
// rule, and they are never required by the completeness check.
const areaNamePrefix = "area.name."

type Catalogue struct {
	messages map[string]map[string]string // lang → key → text
	langs    []string                     // display order: DefaultLang, then sorted
}

// Load reads the embedded catalogues only. Equivalent to LoadWithOverrides("").
func Load() (*Catalogue, error) { return LoadWithOverrides("") }

// LoadWithOverrides reads the embedded catalogues, then reads dir — which both
// overrides keys in an existing language and ADDS languages that have no
// embedded catalogue at all.
//
// That is the whole internationalisation story: to add German, write de.json
// with every key the Bulgarian catalogue has and drop it in. No rebuild, no
// schema change, no code change. /de/ starts serving on the next restart and
// the language switcher grows a link.
//
// An empty dir means embedded only, the same all-or-nothing shape tiles.* uses.
// Catalogues are read ONCE, at startup: re-reading per request would put an
// operator-controlled filesystem read on the hot path of every page, and a
// watcher would need locking around a catalogue that is otherwise immutable and
// shared by every handler. Restart to apply.
func LoadWithOverrides(dir string) (*Catalogue, error) {
	c := &Catalogue{messages: map[string]map[string]string{}}
	if err := c.loadEmbedded(); err != nil {
		return nil, err
	}

	// The required set is snapshotted from the EMBEDDED default catalogue,
	// before any override runs. What a release ships is what a translation
	// owes; an override file cannot enlarge that debt. The unknown-key guard
	// in overlay already stops a bg.json override from introducing a key, so
	// today the two orders agree — this ordering is what keeps them agreeing
	// if that guard is ever relaxed, rather than turning one operator's
	// added key into "every other language is now incomplete", i.e. a site
	// that will not start.
	required := make(map[string]struct{}, len(c.messages[DefaultLang]))
	for key := range c.messages[DefaultLang] {
		required[key] = struct{}{}
	}

	if dir != "" {
		if err := c.overlay(dir, required); err != nil {
			return nil, err
		}
	}

	c.langs = orderLangs(c.messages)
	if err := c.checkComplete(required); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalogue) loadEmbedded() error {
	entries, err := fs.ReadDir(catalogueFS, ".")
	if err != nil {
		return fmt.Errorf("i18n: reading embedded catalogues: %w", err)
	}
	for _, e := range entries {
		lang, ok := langFromFilename(e.Name())
		if !ok {
			continue
		}
		raw, err := catalogueFS.ReadFile(e.Name())
		if err != nil {
			return fmt.Errorf("i18n: reading %s: %w", e.Name(), err)
		}
		m, err := parseCatalogue(lang, raw)
		if err != nil {
			return err
		}
		c.messages[lang] = m
	}
	if _, ok := c.messages[DefaultLang]; !ok {
		// Unreachable while bg.json is committed; the guard is here because
		// every fallback in T bottoms out in this catalogue.
		return fmt.Errorf("i18n: no embedded catalogue for the default language %q", DefaultLang)
	}
	return nil
}

// overlay reads every <lang>.json in dir: an existing language's file overrides
// the keys it names, and a new language's file becomes a new language.
//
// Three things are rejected rather than absorbed, because each one is a
// silently-does-nothing bug in an operator's hands: a filename that is not a
// language code (drop "bulgarian.json" in and nothing would happen), a key the
// default catalogue does not hold and that is not an area name (a typo, or a
// key retired by a later release), and a blank value (which would render an
// empty label, the exact outcome T's "!key!" marker exists to avoid).
//
// A missing directory is an error too: it means the operator configured an
// override path that isn't there, and starting with the embedded copy would
// serve the copy they replaced while looking healthy.
func (c *Catalogue) overlay(dir string, required map[string]struct{}) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("i18n: reading override directory: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		lang, ok := langFromFilename(name)
		if !ok {
			return fmt.Errorf("i18n: override %s does not name a language; expected a code like %q or %q",
				name, DefaultLang, "de-at")
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("i18n: reading override %s: %w", name, err)
		}
		overrides, err := parseCatalogue(lang, raw)
		if err != nil {
			return err
		}
		messages, ok := c.messages[lang]
		if !ok {
			// A language with no embedded catalogue. checkComplete decides
			// whether the file is a whole translation or an unusable fragment.
			messages = map[string]string{}
			c.messages[lang] = messages
		}
		for key, text := range overrides {
			if _, ok := required[key]; !ok && !strings.HasPrefix(key, areaNamePrefix) {
				return fmt.Errorf("i18n: override %s sets unknown key %q", name, key)
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("i18n: override %s sets key %q to a blank value", name, key)
			}
			messages[key] = text
		}
	}
	return nil
}

// checkComplete refuses a language that translates only part of the site.
//
// T falls back to Bulgarian for a missing key, so a half-finished de.json would
// not error or render markers — it would serve a page in two languages at once
// and look deliberate. A translator wants that list of missing keys at startup,
// not a bug report from a reader.
func (c *Catalogue) checkComplete(required map[string]struct{}) error {
	for _, lang := range c.langs {
		if lang == DefaultLang {
			continue
		}
		var missing []string
		for key := range required {
			if _, ok := c.messages[lang][key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) == 0 {
			continue
		}
		sort.Strings(missing)
		shown := missing
		const maxShown = 5
		if len(shown) > maxShown {
			shown = shown[:maxShown]
		}
		return fmt.Errorf("i18n: catalogue %s is missing %d of %d keys: %s",
			lang, len(missing), len(required), strings.Join(shown, ", "))
	}
	return nil
}

// langFromFilename maps "de.json" to "de". Anything else — a README, a .bak, a
// name that is not a language code — is reported as not a catalogue, and the
// caller decides whether to skip it or refuse it.
func langFromFilename(name string) (string, bool) {
	if !strings.HasSuffix(name, ".json") {
		return "", false
	}
	lang := strings.TrimSuffix(name, ".json")
	if !langPattern.MatchString(lang) {
		return "", false
	}
	return lang, true
}

// orderLangs puts the default first — it is the site's own language and the one
// a reader lands on — and sorts the rest, so the switcher's order is stable
// across restarts and does not depend on map iteration.
func orderLangs(messages map[string]map[string]string) []string {
	rest := make([]string, 0, len(messages))
	for lang := range messages {
		if lang != DefaultLang {
			rest = append(rest, lang)
		}
	}
	sort.Strings(rest)
	return append([]string{DefaultLang}, rest...)
}

// parseCatalogue parses and validates a single catalogue's raw JSON bytes,
// rejecting an empty or unparseable catalogue. Factored out of Load so the
// rejection can be exercised directly with injected bytes in tests, without
// needing to corrupt the embedded FS to prove the guard fires.
func parseCatalogue(lang string, raw []byte) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("i18n: parsing %s.json: %w", lang, err)
	}
	if len(m) == 0 {
		// An empty catalogue would render every label as a fallback marker
		// on a live page. Fail at startup instead.
		return nil, fmt.Errorf("i18n: %s.json contains no messages", lang)
	}
	return m, nil
}

// T returns the message for key in lang.
//
// Fallback order: the requested language, then Bulgarian, then a visible marker
// naming the key. Returning "" for a missing key would render a blank where a
// label belongs and leave nothing to search for — a silently broken page.
func (c *Catalogue) T(lang, key string) string {
	if m, ok := c.messages[lang]; ok {
		if text, ok := m[key]; ok {
			return text
		}
	}
	if text, ok := c.messages[DefaultLang][key]; ok {
		return text
	}
	return "!" + key + "!"
}

func (c *Catalogue) Has(lang, key string) bool {
	m, ok := c.messages[lang]
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// Languages returns the served set in display order. A copy: the slice is
// handed to templates, and a caller sorting it in place would reorder every
// page's language switcher.
func (c *Catalogue) Languages() []string {
	out := make([]string, len(c.langs))
	copy(out, c.langs)
	return out
}

// Keys returns the union of every catalogue's keys, sorted. Used by the
// consistency test — the union rather than one language's keys, so a key present
// only in English is caught too.
func (c *Catalogue) Keys() []string {
	seen := map[string]struct{}{}
	for _, m := range c.messages {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LangFromPath splits a request path into its language and the remainder.
//
// Matching on the full first segment, not a prefix: strings.HasPrefix(path,
// "/en") would classify "/energy" as English and then serve "ergy" as the path.
//
// A method, not a package function: the served set is whatever catalogues
// loaded, so routing cannot be decided from a compiled-in list.
func (c *Catalogue) LangFromPath(path string) (string, string) {
	for _, lang := range c.langs {
		if lang == DefaultLang {
			continue
		}
		if path == "/"+lang || path == "/"+lang+"/" {
			return lang, "/"
		}
		if strings.HasPrefix(path, "/"+lang+"/") {
			return lang, strings.TrimPrefix(path, "/"+lang)
		}
	}
	return DefaultLang, path
}
