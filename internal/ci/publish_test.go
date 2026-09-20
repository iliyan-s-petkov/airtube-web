// This test asserts the security invariants of .github/workflows/publish.yml
// the same way ci_test.go asserts them for ci.yml: publish.yml lives under a
// dot-prefixed directory the Go tool skips, so nothing else in this
// repository can look at it, and a plausible edit could break any of these
// silently.
package ci

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const publishWorkflowPath = "../../.github/workflows/publish.yml"

func readWorkflowFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// TestPublishPermissionsAreExact pins the top-level permissions block to
// exactly the three keys the workflow needs: contents: read to check out,
// packages: write to push to GHCR, id-token: write for cosign's keyless OIDC
// flow. Any other key, or a missing one, changes the blast radius of the
// GITHUB_TOKEN this workflow runs with.
func TestPublishPermissionsAreExact(t *testing.T) {
	raw := readWorkflowFile(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	permIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "permissions:" {
			permIdx = i
			break
		}
	}
	if permIdx == -1 {
		t.Fatalf("%s has no top-level `permissions:` block", publishWorkflowPath)
	}

	want := map[string]string{
		"contents": "read",
		"packages": "write",
		"id-token": "write",
	}
	got := map[string]string{}
	keyLine := regexp.MustCompile(`^\s{2}([\w-]+):\s*(\S+)\s*$`)
	for _, line := range lines[permIdx+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		if m == nil {
			// A 2-space-indented, non-matching line means the permissions
			// block ended (next top-level key at column 0 or a job key).
			break
		}
		got[m[1]] = m[2]
	}

	if len(got) != len(want) {
		t.Fatalf("%s top-level permissions has %d keys (%v), want exactly %v", publishWorkflowPath, len(got), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s permissions.%s = %q, want %q", publishWorkflowPath, k, got[k], v)
		}
	}
}

// pinnedUses matches a `uses: owner/repo@ref` line and captures the ref, so
// the test can report which action is unpinned rather than just that one is.
var pinnedUses = regexp.MustCompile(`uses:\s*\S+@(\S+)`)

// fullSHA is what an acceptable pin looks like: a 40-character hex commit
// SHA. A tag (`v3`), a branch, or a short SHA all fail — a tag or branch is
// a mutable pointer an upstream account compromise can repoint.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestPublishActionsArePinnedToFullSHA proves every `uses:` step in
// publish.yml is pinned to an immutable commit, matching the pin form 7.1
// established for ci.yml.
func TestPublishActionsArePinnedToFullSHA(t *testing.T) {
	raw := readWorkflowFile(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	usesCount := 0
	for _, line := range lines {
		if !strings.Contains(line, "uses:") {
			continue
		}
		usesCount++
		m := pinnedUses.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("%s: could not parse a ref out of `uses:` line: %q", publishWorkflowPath, line)
			continue
		}
		if !fullSHA.MatchString(m[1]) {
			t.Errorf("%s: %q is pinned to %q, not a 40-char commit SHA", publishWorkflowPath, strings.TrimSpace(line), m[1])
		}
	}

	// Positive control: a scan that matches zero `uses:` lines would pass
	// identically whether every action is pinned or the workflow has none at
	// all.
	if usesCount == 0 {
		t.Fatalf("found no `uses:` lines in %s; this test is scanning nothing", publishWorkflowPath)
	}
}

// TestTrivyRunsBeforeCosignSign proves the ordering the brief calls a
// security property: a Trivy failure must leave the image unsigned, so
// Task 7.5's verify step refuses it. It also pins exit-code: '1', without
// which a Trivy finding would not fail the job at all.
func TestTrivyRunsBeforeCosignSign(t *testing.T) {
	raw := readWorkflowFile(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	trivyIdx, exitCodeIdx, cosignSignIdx := -1, -1, -1
	for i, line := range lines {
		switch {
		case trivyIdx == -1 && containsAll(line, []string{"uses:", "aquasecurity/trivy-action"}):
			trivyIdx = i
		case exitCodeIdx == -1 && containsAll(line, []string{"exit-code:", "'1'"}):
			exitCodeIdx = i
		case cosignSignIdx == -1 && containsAll(line, []string{"cosign sign"}):
			cosignSignIdx = i
		}
	}

	if trivyIdx == -1 {
		t.Fatalf("%s: no `uses: aquasecurity/trivy-action` step found", publishWorkflowPath)
	}
	if cosignSignIdx == -1 {
		t.Fatalf("%s: no `cosign sign` step found", publishWorkflowPath)
	}
	if exitCodeIdx == -1 {
		t.Errorf("%s: trivy step has no `exit-code: '1'`; a HIGH/CRITICAL finding would not fail the job", publishWorkflowPath)
	}
	if !(trivyIdx < cosignSignIdx) {
		t.Errorf("%s: trivy step (line %d) is not before `cosign sign` (line %d); a scan failure must leave the image unsigned",
			publishWorkflowPath, trivyIdx, cosignSignIdx)
	}
}

// cosignSignsDigest matches the cosign sign invocation and requires it to
// target an `@sha256:...`-style digest reference built from the build step's
// digest output, never a mutable `:tag`.
var cosignSignsDigest = regexp.MustCompile(`cosign sign --yes \S+@\$\{\{\s*steps\.build\.outputs\.digest\s*\}\}`)

// TestCosignSignsTheDigestNotATag proves cosign signs the immutable digest
// docker/build-push-action produced, not a tag another push could repoint.
func TestCosignSignsTheDigestNotATag(t *testing.T) {
	raw := readWorkflowFile(t, publishWorkflowPath)
	if !cosignSignsDigest.MatchString(raw) {
		t.Errorf("%s: no `cosign sign` step found targeting `@${{ steps.build.outputs.digest }}`", publishWorkflowPath)
	}
}
