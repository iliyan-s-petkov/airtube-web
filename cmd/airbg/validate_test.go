package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/config"
)

func TestValidateConfigAcceptsCommittedFile(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 0 {
		t.Fatalf("runValidateConfig = %d, want 0; stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "configuration is valid") {
		t.Errorf("stdout does not confirm validity:\n%s", out.String())
	}
}

// The command must never print a credential: it runs in CI logs.
func TestValidateConfigNeverPrintsSecrets(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:hunter2@localhost:5432/airbg")
	t.Setenv(config.BasemapKeyEnv, "s3cr3tk3y")
	var out, errOut bytes.Buffer
	runValidateConfig(&out, &errOut)
	combined := out.String() + errOut.String()
	for _, secret := range []string{"hunter2", "s3cr3tk3y"} {
		if strings.Contains(combined, secret) {
			t.Errorf("output contains the secret %q:\n%s", secret, combined)
		}
	}
}

func TestValidateConfigRejectsMissingFile(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join(t.TempDir(), "absent.yaml"))
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "cannot read") {
		t.Errorf("stderr = %q, want it to name the unreadable file", errOut.String())
	}
}

// A file that exists and parses but is semantically invalid must still be
// rejected: the gate exists precisely to catch this class, not just missing
// files or unset paths. The fixture is a full copy of the committed
// airbg.yaml with ratelimit.api.per_second changed from its real value (10)
// to 0, so a hardcoded default cannot make this pass for the wrong reason.
func TestValidateConfigRejectsSemanticallyInvalidFile(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("testdata", "invalid-ratelimit.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1; stdout:\n%s", code, out.String())
	}
	const want = "ratelimit.api.per_second = 0, must be greater than zero"
	if !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), want)
	}
}

// AIRBG_BASEMAP_KEY is substituted into basemap.style_url's query string
// before Validate ever runs. A key containing bytes that make the resulting
// URL unparseable (e.g. a trailing control character) must not resurface via
// net/url's parse-error message, which otherwise quotes the whole input URL,
// query string included.
func TestValidateConfigNeverPrintsSecretThatBreaksBasemapURL(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	const secret = "s3cr3tk3y\x7f"
	t.Setenv(config.BasemapKeyEnv, secret)
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1 (the substituted key makes basemap.style_url unparseable); stdout:\n%s", code, out.String())
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, secret) || strings.Contains(combined, "s3cr3tk3y") {
		t.Errorf("output contains the basemap key:\n%s", combined)
	}
	if !strings.Contains(errOut.String(), "basemap.style_url is not a URL") {
		t.Errorf("stderr = %q, want it to still name what is wrong with basemap.style_url", errOut.String())
	}
}

func TestValidateConfigRejectsUnsetPath(t *testing.T) {
	t.Setenv(config.PathEnv, "")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), config.PathEnv) {
		t.Errorf("stderr = %q, want it to name %s", errOut.String(), config.PathEnv)
	}
}
