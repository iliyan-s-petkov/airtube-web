package i18n_test

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

func loaded(t *testing.T) *i18n.Catalogue {
	t.Helper()
	c, err := i18n.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestTranslatesBothLanguages(t *testing.T) {
	c := loaded(t)

	bg := c.T("bg", "site.title")
	en := c.T("en", "site.title")

	if bg == "" || en == "" {
		t.Fatalf("site.title is empty (bg=%q en=%q)", bg, en)
	}
	if bg == en {
		t.Errorf("bg and en are identical (%q); one of the catalogues is untranslated", bg)
	}
	// Bulgarian must actually be Cyrillic — a catalogue accidentally filled with
	// the English strings would pass every other assertion here.
	if !strings.ContainsAny(bg, "абвгдежзийклмнопрстуфхцчшщъьюяАБВГДЕЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЮЯ") {
		t.Errorf("bg site.title = %q contains no Cyrillic", bg)
	}
}

// TestCataloguesHaveIdenticalKeys is the test that keeps translations honest.
// A key present in bg.json and missing from en.json renders as a fallback on
// every English page — visible to users, invisible in tests that only check the
// keys they happen to name.
func TestCataloguesHaveIdenticalKeys(t *testing.T) {
	c := loaded(t)

	for _, key := range c.Keys() {
		for _, lang := range i18n.Languages {
			if !c.Has(lang, key) {
				t.Errorf("key %q is missing from the %q catalogue", key, lang)
			}
		}
	}
}

// TestMissingKeyFallsBackVisibly: an unknown key must not render as an empty
// string. An empty string produces a page with a blank where a label belongs and
// nothing in the logs — the failure mode is a silently broken UI.
func TestMissingKeyFallsBackVisibly(t *testing.T) {
	c := loaded(t)

	got := c.T("en", "no.such.key")
	if got == "" {
		t.Fatal("a missing key rendered as an empty string")
	}
	if !strings.Contains(got, "no.such.key") {
		t.Errorf("the fallback %q does not name the missing key, so nobody can find it", got)
	}
}

// TestUnknownLanguageFallsBackToBulgarian rather than to an empty catalogue.
func TestUnknownLanguageFallsBackToBulgarian(t *testing.T) {
	c := loaded(t)

	if got, want := c.T("de", "site.title"), c.T("bg", "site.title"); got != want {
		t.Errorf("T(\"de\", …) = %q, want the Bulgarian %q", got, want)
	}
}

func TestLangFromPath(t *testing.T) {
	cases := []struct{ path, lang, rest string }{
		{"/", "bg", "/"},
		{"/area/sofia", "bg", "/area/sofia"},
		{"/en/", "en", "/"},
		{"/en/area/sofia", "en", "/area/sofia"},
		// "/en" with no trailing slash is still the English root.
		{"/en", "en", "/"},
		// A path that merely starts with the letters "en" is not English.
		{"/energy", "bg", "/energy"},
		// An unsupported prefix is part of the path, not a language.
		{"/de/area/sofia", "bg", "/de/area/sofia"},
	}
	for _, c := range cases {
		lang, rest := i18n.LangFromPath(c.path)
		if lang != c.lang || rest != c.rest {
			t.Errorf("LangFromPath(%q) = (%q, %q), want (%q, %q)", c.path, lang, rest, c.lang, c.rest)
		}
	}
}
