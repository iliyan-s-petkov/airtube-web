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
