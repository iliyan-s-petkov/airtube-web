package i18n_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// writeOverrides builds an override directory and returns its path.
func writeOverrides(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

// An empty dir is the shipped setting: no override directory is consulted and
// the embedded catalogues are served unchanged.
func TestNoOverrideDirectoryServesTheEmbeddedCatalogues(t *testing.T) {
	c, err := i18n.LoadWithOverrides("")
	if err != nil {
		t.Fatalf("LoadWithOverrides: %v", err)
	}
	if got, want := c.T("bg", "locate.button"), loaded(t).T("bg", "locate.button"); got != want {
		t.Errorf("T = %q, want the embedded %q", got, want)
	}
}

// The point of the feature: reword one sentence without touching the binary.
func TestOverrideReplacesOnlyTheKeysItNames(t *testing.T) {
	embedded := loaded(t)
	dir := writeOverrides(t, map[string]string{
		"bg.json": `{"locate.button": "Открий ме"}`,
	})

	c, err := i18n.LoadWithOverrides(dir)
	if err != nil {
		t.Fatalf("LoadWithOverrides: %v", err)
	}

	if got := c.T("bg", "locate.button"); got != "Открий ме" {
		t.Errorf("overridden key = %q, want the override", got)
	}
	// Untouched keys keep the embedded text — a partial file must not blank
	// the rest of the catalogue.
	if got, want := c.T("bg", "locate.failed"), embedded.T("bg", "locate.failed"); got != want {
		t.Errorf("untouched key = %q, want the embedded %q", got, want)
	}
	// And the other language is untouched by a bg-only override.
	if got, want := c.T("en", "locate.button"), embedded.T("en", "locate.button"); got != want {
		t.Errorf("en key = %q, want the embedded %q", got, want)
	}
}

// Each of these is a way an operator's edit could silently do nothing (or
// render an empty label). All of them must stop the process at startup, where
// a human is watching, rather than reach a visitor.
func TestOverrideRejections(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "unsupported language",
			files:   map[string]string{"de.json": `{"locate.button": "Finde mich"}`},
			wantErr: `names language "de"`,
		},
		{
			name:    "unknown key",
			files:   map[string]string{"bg.json": `{"locate.buton": "Открий ме"}`},
			wantErr: `unknown key "locate.buton"`,
		},
		{
			name:    "blank value",
			files:   map[string]string{"bg.json": `{"locate.button": "   "}`},
			wantErr: `blank value`,
		},
		{
			name:    "unparseable json",
			files:   map[string]string{"bg.json": `{"locate.button":`},
			wantErr: "parsing bg.json",
		},
		{
			name:    "empty catalogue",
			files:   map[string]string{"bg.json": `{}`},
			wantErr: "contains no messages",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := i18n.LoadWithOverrides(writeOverrides(t, tc.files))
			if err == nil {
				t.Fatalf("LoadWithOverrides accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// A configured directory that is not there means the operator meant to serve
// overridden copy and isn't. Falling back to the embedded catalogue would look
// healthy while serving the words they replaced.
func TestMissingOverrideDirectoryIsAStartupError(t *testing.T) {
	_, err := i18n.LoadWithOverrides(filepath.Join(t.TempDir(), "not-there"))
	if err == nil {
		t.Fatal("LoadWithOverrides accepted a directory that does not exist")
	}
	if !strings.Contains(err.Error(), "override directory") {
		t.Errorf("error = %q, want it to name the override directory", err)
	}
}

// Non-JSON files are the operator's own notes, backups and editor droppings.
// Ignoring them is deliberate; ignoring a .json file would not be.
func TestNonJSONFilesAreIgnored(t *testing.T) {
	dir := writeOverrides(t, map[string]string{
		"README.md":   "how we word things",
		"bg.json.bak": `{"nonsense": "would be rejected if this were read"}`,
		"bg.json":     `{"locate.button": "Открий ме"}`,
	})

	c, err := i18n.LoadWithOverrides(dir)
	if err != nil {
		t.Fatalf("LoadWithOverrides: %v", err)
	}
	if got := c.T("bg", "locate.button"); got != "Открий ме" {
		t.Errorf("T = %q, want the override to have applied", got)
	}
}
