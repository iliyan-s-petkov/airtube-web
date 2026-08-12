package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTemp puts a config body in a temp file and returns its path.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airbg.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	return path
}

// A key present in the schema but absent from the file must be an error naming
// that key. This is the whole point of the pointer schema: with no defaults in
// code, an absent key that decoded to zero would give an unlimited listener or a
// one-sensor oblast.
func TestReadRawReportsMissingKeys(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an error listing missing keys")
	}
	for _, want := range []string{"listen.metrics_addr", "quality.ranges.pressure.min", "series.periods"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention missing key %q", err, want)
		}
	}
}

// The error must list every missing key at once. An operator fixing a 40-key
// file one restart at a time is a loader bug, not an operator problem.
func TestReadRawListsAllMissingKeysAtOnce(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an error")
	}
	if n := strings.Count(err.Error(), "\n"); n < 20 {
		t.Errorf("error reports %d lines, want at least 20 missing keys listed together:\n%s", n+1, err)
	}
}

func TestReadRawRejectsUnknownKey(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr_typo: \"x\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an unknown-field error")
	}
	if !strings.Contains(err.Error(), "addr_typo") {
		t.Errorf("error %q does not name the unknown key addr_typo", err)
	}
}

// Secrets must be rejected on sight, not ignored. An ignored database_url in a
// committed file is a credential in git that appears to be in use.
func TestRejectSecrets(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"database_url at root", "database_url: \"postgres://u:p@h/db\"\n"},
		{"url under database", "database:\n  url: \"postgres://u:p@h/db\"\n"},
		{"dsn", "database:\n  dsn: \"postgres://u:p@h/db\"\n"},
		{"password", "database:\n  password: \"hunter2\"\n"},
		{"basemap key", "basemap:\n  key: \"abc123\"\n"},
		{"basemap_key at root", "basemap_key: \"abc123\"\n"},
		{"api_key", "basemap:\n  api_key: \"abc123\"\n"},
		{"token", "upstream:\n  token: \"abc123\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectSecrets([]byte(tt.body))
			if err == nil {
				t.Fatalf("rejectSecrets(%q) error = nil, want a rejection", tt.body)
			}
			if !strings.Contains(err.Error(), "environment") {
				t.Errorf("error %q should tell the operator to use an environment variable", err)
			}
		})
	}
}

// The committed file must not trip the secret scanner.
func TestRejectSecretsAllowsCommittedFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if err := rejectSecrets(data); err != nil {
		t.Errorf("rejectSecrets(airbg.yaml) error = %v, want nil", err)
	}
}

func TestEnvName(t *testing.T) {
	tests := []struct{ path, want string }{
		{"listen.addr", "AIRBG_LISTEN_ADDR"},
		{"listen.metrics_addr", "AIRBG_LISTEN_METRICS_ADDR"},
		{"upstream.poll_interval", "AIRBG_UPSTREAM_POLL_INTERVAL"},
		{"quality.ranges.pressure.min", "AIRBG_QUALITY_RANGES_PRESSURE_MIN"},
		{"quality.ranges.noise_LAeq.max", "AIRBG_QUALITY_RANGES_NOISE_LAEQ_MAX"},
	}
	for _, tt := range tests {
		if got := envName(tt.path); got != tt.want {
			t.Errorf("envName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// The full committed file must produce no missing keys. This is the guard that
// catches a schema field added without a corresponding file key.
func TestCommittedConfigHasEveryKey(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw(airbg.yaml) error = %v, want nil", err)
	}
	if missing := missingKeys(reflect.ValueOf(r).Elem(), ""); len(missing) != 0 {
		t.Errorf("airbg.yaml is missing %d keys: %v", len(missing), missing)
	}
}
