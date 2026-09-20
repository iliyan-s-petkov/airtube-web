package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gcpAPIKey is gitleaks' own shape for a Google API key. The 2017 PHP
// collector committed one; it was later quoted verbatim in ANALYSIS.md.
var gcpAPIKey = regexp.MustCompile(`AIzaSy[\w-]{33}`)

// TestSecretScanRunsOnEveryEvent: ci.yml has a job that runs gitleaks, pinned
// to a full commit SHA, on a full-history checkout, with no `if:` gating it
// off any event. A gate is the only way a secret job can be green and inert.
func TestSecretScanRunsOnEveryEvent(t *testing.T) {
	lines := strings.Split(readWorkflow(t, workflowPath), "\n")

	jobStart := regexp.MustCompile(`^  [\w-]+:\s*$`)
	gitleaksIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "uses: gitleaks/gitleaks-action@") {
			gitleaksIdx = i
		}
	}
	if gitleaksIdx == -1 {
		t.Fatalf("no gitleaks/gitleaks-action step in %s", workflowPath)
	}
	m := pinnedUses.FindStringSubmatch(lines[gitleaksIdx])
	if m == nil || !fullSHA.MatchString(m[1]) {
		t.Errorf("%s: %q is not pinned to a 40-char commit SHA", workflowPath, strings.TrimSpace(lines[gitleaksIdx]))
	}

	// Walk back to the job header; everything between is this job's config.
	start := gitleaksIdx
	for start > 0 && !jobStart.MatchString(lines[start]) {
		start--
	}
	job := strings.Join(lines[start:gitleaksIdx+1], "\n")
	if strings.Contains(job, "if:") {
		t.Errorf("the gitleaks job in %s carries an `if:`; the scan must run on every push and pull request", workflowPath)
	}
	if !strings.Contains(job, "fetch-depth: 0") {
		t.Errorf("the gitleaks job in %s checks out without `fetch-depth: 0`; a shallow clone scans only the tip commit", workflowPath)
	}
}

// TestNoGoogleAPIKeyInTrackedFiles: the leaked key is allowlisted by
// fingerprint in .gitleaksignore (history cannot be rewritten on a public
// repo), so nothing stops a fresh paste of it except this scan of the
// tracked tree. Fingerprints carry no secret bytes, so the ignore file is
// scanned too.
func TestNoGoogleAPIKeyInTrackedFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 100 {
		t.Fatalf("git ls-files returned %d paths; expected the whole repository", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue // submodule or deleted-but-staged path
		}
		if loc := gcpAPIKey.FindIndex(raw); loc != nil {
			line := 1 + strings.Count(string(raw[:loc[0]]), "\n")
			t.Errorf("%s:%d contains a Google API key; remove it, and revoke it if it is live", f, line)
		}
	}
}

// TestGitleaksIgnoreHoldsOnlyFingerprints: every non-comment line of
// .gitleaksignore is `<sha>:<path>:<rule>:<line>`. A bare path or rule name
// there would allowlist a whole file or a whole class of findings.
func TestGitleaksIgnoreHoldsOnlyFingerprints(t *testing.T) {
	raw := readWorkflow(t, filepath.Join("..", "..", ".gitleaksignore"))
	fingerprint := regexp.MustCompile(`^[0-9a-f]{40}:[^:\s]+:[\w-]+:\d+$`)
	n := 0
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		if !fingerprint.MatchString(line) {
			t.Errorf(".gitleaksignore:%d: %q is not a commit-scoped fingerprint", i+1, line)
		}
	}
	if n == 0 {
		t.Fatal(".gitleaksignore has no fingerprints; the two known findings would fail the scan")
	}
}
