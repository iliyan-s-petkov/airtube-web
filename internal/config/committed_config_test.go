package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoConfigPath locates the committed airbg.yaml from inside the package
// directory. Tests run with the package directory as CWD, so the repo root is
// two levels up from internal/config.
func repoConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "airbg.yaml")
}

// The committed file must decode strictly against the schema: no unknown keys,
// every value the right type, every duration a parseable string. If this fails,
// the shipped configuration is broken for every operator.
//
// This test reads the real committed file rather than a fixture on purpose. A
// fixture copy is how a shipped config file rots.
func TestCommittedConfigDecodesStrictly(t *testing.T) {
	data, err := os.ReadFile(repoConfigPath(t))
	if err != nil {
		t.Fatalf("ReadFile(airbg.yaml) error = %v, want nil", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var r raw
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("Decode(airbg.yaml) error = %v, want nil", err)
	}
	// Spot-check one leaf per depth so a whole group silently decoding to nil
	// cannot pass. Full completeness is Task 4's missingKeys walk.
	if r.Listen == nil || r.Listen.Addr == nil {
		t.Fatal("listen.addr decoded to nil, want a value")
	}
	if got, want := *r.Listen.Addr, "127.0.0.1:8080"; got != want {
		t.Errorf("listen.addr = %q, want %q", got, want)
	}
	if r.Quality == nil || r.Quality.Ranges == nil || r.Quality.Ranges.Pressure == nil {
		t.Fatal("quality.ranges.pressure decoded to nil, want a value")
	}
	if got, want := *r.Quality.Ranges.Pressure.Min, 650.0; got != want {
		t.Errorf("quality.ranges.pressure.min = %v, want %v", got, want)
	}
	if len(r.Series.Periods) != 4 {
		t.Errorf("len(series.periods) = %d, want 4", len(r.Series.Periods))
	}
}
