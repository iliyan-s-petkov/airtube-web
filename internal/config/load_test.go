package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

// readRaw must reject secrets at the file level, not just accept valid files.
// This test proves the rejectSecrets call inside readRaw is actually wired.
func TestReadRawRejectsSecretsViaSecretKeys(t *testing.T) {
	path := writeTemp(t, "basemap:\n  key: \"abc123\"\nlisten:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw with basemap.key secret error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "basemap.key") {
		t.Errorf("error %q does not name basemap.key", err)
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Errorf("error %q does not mention environment variable", err)
	}
}

// readRaw must reject full-path secret matches (database.url), proving the
// secretPaths wiring is integrated into readRaw.
func TestReadRawRejectsSecretsViaSecretPaths(t *testing.T) {
	path := writeTemp(t, "database:\n  url: \"postgres://u:p@h/db\"\nlisten:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw with database.url secret error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "database.url") {
		t.Errorf("error %q does not name database.url", err)
	}
	if !strings.Contains(err.Error(), "AIRBG_DATABASE_URL") {
		t.Errorf("error %q does not mention AIRBG_DATABASE_URL", err)
	}
}

// The mechanical rule must hold for every scalar kind, not just strings.
func TestApplyEnvOverridesEveryScalarKind(t *testing.T) {
	path := filepath.Join("..", "..", "airbg.yaml")

	t.Setenv("AIRBG_LISTEN_ADDR", "0.0.0.0:9999")
	t.Setenv("AIRBG_LISTEN_MAX_CONNS", "512")
	t.Setenv("AIRBG_UPSTREAM_POLL_INTERVAL", "11m")
	t.Setenv("AIRBG_QUALITY_MAD_SCALE", "2.5")
	t.Setenv("AIRBG_STORE_COVERAGE_THRESHOLD", "7")
	t.Setenv("AIRBG_UPSTREAM_MAX_PAYLOAD_BYTES", "1024")
	t.Setenv("AIRBG_LISTEN_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,192.168.0.0/16")

	r, err := readRaw(path)
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	if got, want := *r.Listen.Addr, "0.0.0.0:9999"; got != want {
		t.Errorf("listen.addr = %q, want %q", got, want)
	}
	if got, want := *r.Listen.MaxConns, int32(512); got != want {
		t.Errorf("listen.max_conns = %d, want %d", got, want)
	}
	if got, want := r.Upstream.PollInterval.Std(), 11*time.Minute; got != want {
		t.Errorf("upstream.poll_interval = %v, want %v", got, want)
	}
	if got, want := *r.Quality.MADScale, 2.5; got != want {
		t.Errorf("quality.mad_scale = %v, want %v", got, want)
	}
	if got, want := *r.Store.CoverageThreshold, 7; got != want {
		t.Errorf("store.coverage_threshold = %d, want %d", got, want)
	}
	if got, want := *r.Upstream.MaxPayloadBytes, int64(1024); got != want {
		t.Errorf("upstream.max_payload_bytes = %d, want %d", got, want)
	}
	if got, want := len(*r.Listen.TrustedProxyCIDRs), 2; got != want {
		t.Fatalf("len(listen.trusted_proxy_cidrs) = %d, want %d", got, want)
	}
	if got, want := (*r.Listen.TrustedProxyCIDRs)[1], "192.168.0.0/16"; got != want {
		t.Errorf("listen.trusted_proxy_cidrs[1] = %q, want %q", got, want)
	}
}

// A nested leaf three levels down must follow the same rule.
func TestApplyEnvOverridesNestedLeaf(t *testing.T) {
	t.Setenv("AIRBG_QUALITY_RANGES_PRESSURE_MIN", "700")
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	if got, want := *r.Quality.Ranges.Pressure.Min, 700.0; got != want {
		t.Errorf("quality.ranges.pressure.min = %v, want %v", got, want)
	}
}

// An unparseable override must be a startup error naming the variable, never a
// silently-ignored value that leaves the file's setting in place.
func TestApplyEnvRejectsGarbage(t *testing.T) {
	t.Setenv("AIRBG_LISTEN_MAX_CONNS", "many")
	_, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err == nil {
		t.Fatal("readRaw error = nil, want an error for AIRBG_LISTEN_MAX_CONNS=many")
	}
	if !strings.Contains(err.Error(), "AIRBG_LISTEN_MAX_CONNS") {
		t.Errorf("error %q does not name the offending variable", err)
	}
}

// The environment can supply a key the file omits entirely — the two layers are
// file then environment, and either may be the source of a value.
func TestApplyEnvSuppliesAbsentKey(t *testing.T) {
	body := "listen:\n  metrics_addr: \"127.0.0.1:9090\"\n"
	path := writeTemp(t, body)
	t.Setenv("AIRBG_LISTEN_ADDR", "127.0.0.1:8080")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want a missing-key error for the other keys")
	}
	if strings.Contains(err.Error(), "listen.addr") {
		t.Errorf("listen.addr was supplied by the environment but still reported missing:\n%s", err)
	}
}

// AIRBG_DATABASE_URL_FILE exists for ofelia's one-shot `collect` job, which is
// started fresh through the Docker API and inherits no compose env_file. It
// must actually deliver the credential, not just be accepted.
func TestDatabaseURLFileIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte("postgres://user:pass@db:5432/airbg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	t.Setenv(DatabaseURLFileEnv, path)
	got, err := databaseURLFromEnv()
	if err != nil {
		t.Fatalf("databaseURLFromEnv error = %v, want nil", err)
	}
	if want := "postgres://user:pass@db:5432/airbg"; got != want {
		t.Errorf("databaseURLFromEnv() = %q, want %q (trailing whitespace must be trimmed)", got, want)
	}
}

// AIRBG_DATABASE_URL is the credential itself, not a path, so it must win over
// AIRBG_DATABASE_URL_FILE when both happen to be set — the direct value is
// never shadowed by a stale or wrong file path.
func TestDatabaseURLEnvTakesPrecedenceOverFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte("postgres://from-file/db\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	t.Setenv(DatabaseURLFileEnv, path)
	t.Setenv(DatabaseURLEnv, "postgres://from-env/db")
	got, err := databaseURLFromEnv()
	if err != nil {
		t.Fatalf("databaseURLFromEnv error = %v, want nil", err)
	}
	if want := "postgres://from-env/db"; got != want {
		t.Errorf("databaseURLFromEnv() = %q, want %q", got, want)
	}
}

// A configured file that cannot be read must be a startup error naming both
// the variable and the path — the same reasoning as PGPASSFILE's permission
// requirement in deploy/ofelia.ini: a broken secret must fail loudly, not
// silently start the collector with an empty credential.
func TestDatabaseURLFileMissingIsAnError(t *testing.T) {
	t.Setenv(DatabaseURLFileEnv, filepath.Join(t.TempDir(), "does-not-exist"))
	_, err := databaseURLFromEnv()
	if err == nil {
		t.Fatal("databaseURLFromEnv error = nil, want an error for an unreadable file")
	}
	if !strings.Contains(err.Error(), DatabaseURLFileEnv) {
		t.Errorf("error %q does not mention %s", err, DatabaseURLFileEnv)
	}
}

// An empty credential file must say so. Before this, an operator who created
// /srv/airbg/airbg_database_url but never wrote the DSN into it got
// "AIRBG_DATABASE_URL is not set in the environment; it is required" from
// Validate — describing a variable they had never touched, not the empty file
// they had just made. The message must name AIRBG_DATABASE_URL_FILE, name the
// path, and say the file is empty; and it must not send the reader after
// AIRBG_DATABASE_URL, which was not the problem.
func TestDatabaseURLFileEmptyIsAnErrorNamingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airbg_database_url")
	// Whitespace only: TrimSpace makes this indistinguishable from truly empty,
	// and it is the likelier real-world mistake (an editor leaving a newline).
	if err := os.WriteFile(path, []byte("  \n\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DatabaseURLEnv, "")
	t.Setenv(DatabaseURLFileEnv, path)

	_, err := databaseURLFromEnv()
	if err == nil {
		t.Fatal("databaseURLFromEnv error = nil, want an error for an empty credential file")
	}
	msg := err.Error()
	if !strings.Contains(msg, DatabaseURLFileEnv) {
		t.Errorf("error %q does not mention %s, so it does not point at the variable that was actually set", msg, DatabaseURLFileEnv)
	}
	if !strings.Contains(msg, path) {
		t.Errorf("error %q does not mention the path %s, so the operator cannot tell which file is empty", msg, path)
	}
	if !strings.Contains(msg, "empty") {
		t.Errorf("error %q does not say the file is empty; that is the one fact the operator needs", msg)
	}
	// The old message blamed AIRBG_DATABASE_URL for being unset. Mentioning it
	// alone is not wrong (AIRBG_DATABASE_URL_FILE contains it as a prefix), so
	// this checks for the misleading claim itself.
	if strings.Contains(msg, "is not set") {
		t.Errorf("error %q still claims something is not set; %s was set, and it named a file that exists", msg, DatabaseURLFileEnv)
	}
}

// LoadFile must actually wire databaseURLFromEnv in, not just have it
// available unused — this proves AIRBG_DATABASE_URL_FILE reaches Config
// through the real entry point ofelia's collect job invokes.
func TestLoadFileAcceptsDatabaseURLFile(t *testing.T) {
	clearAmbientEnv(t)
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte("postgres://user:pass@db:5432/airbg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	t.Setenv(DatabaseURLFileEnv, path)
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	if want := "postgres://user:pass@db:5432/airbg"; cfg.Database.URL != want {
		t.Errorf("cfg.Database.URL = %q, want %q", cfg.Database.URL, want)
	}
}
