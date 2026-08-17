// Package i18n serves the UI message catalogues.
//
// Bulgarian is the default and English lives under /en/ (Phase 1 §9.5). The
// catalogues are embedded, so a missing file is a build error rather than a
// deployment that starts fine and renders blank labels.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed bg.json en.json
var catalogueFS embed.FS

const DefaultLang = "bg"

// Languages is the supported set, in display order.
var Languages = []string{"bg", "en"}

type Catalogue struct {
	messages map[string]map[string]string // lang → key → text
}

func Load() (*Catalogue, error) {
	c := &Catalogue{messages: make(map[string]map[string]string, len(Languages))}
	for _, lang := range Languages {
		raw, err := catalogueFS.ReadFile(lang + ".json")
		if err != nil {
			return nil, fmt.Errorf("i18n: reading %s.json: %w", lang, err)
		}
		m, err := parseCatalogue(lang, raw)
		if err != nil {
			return nil, err
		}
		c.messages[lang] = m
	}
	return c, nil
}

// LoadWithOverrides loads the embedded catalogues and then overlays per-key
// overrides read from dir, so an operator can reword copy — a clumsy sentence,
// a translation a native speaker rejected — by editing a file and restarting,
// without a rebuild and a redeploy of the binary.
//
// An empty dir means embedded only, the same all-or-nothing shape tiles.* uses.
// Overrides are read ONCE, at startup: re-reading per request would put an
// operator-controlled filesystem read on the hot path of every page, and a
// watcher would need locking around a catalogue that is otherwise immutable and
// shared by every handler. Restart to apply.
func LoadWithOverrides(dir string) (*Catalogue, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return c, nil
	}
	if err := c.overlay(dir); err != nil {
		return nil, err
	}
	return c, nil
}

// overlay applies every <lang>.json in dir over the already-loaded catalogues.
//
// Three things are rejected rather than absorbed, because each one is a
// silently-does-nothing bug in an operator's hands: a file naming an
// unsupported language (drop de.json in and nothing would happen), a key the
// shipped catalogue does not hold (a typo, or a key retired by a later
// release), and a blank value (which would render an empty label, the exact
// outcome T's "!key!" marker exists to avoid).
//
// A missing directory is an error too: it means the operator configured an
// override path that isn't there, and starting with the embedded copy would
// serve the wrong copy while looking healthy.
func (c *Catalogue) overlay(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("i18n: reading override directory: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		lang := strings.TrimSuffix(name, ".json")
		messages, ok := c.messages[lang]
		if !ok {
			return fmt.Errorf("i18n: override %s names language %q, not one of %s",
				name, lang, strings.Join(Languages, ", "))
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("i18n: reading override %s: %w", name, err)
		}
		overrides, err := parseCatalogue(lang, raw)
		if err != nil {
			return err
		}
		for key, text := range overrides {
			if _, ok := messages[key]; !ok {
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
func LangFromPath(path string) (string, string) {
	for _, lang := range Languages {
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
