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
	var out, errOut bytes.Buffer
	runValidateConfig(&out, &errOut)
	combined := out.String() + errOut.String()
	for _, secret := range []string{"hunter2"} {
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
