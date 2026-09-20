// This test asserts the security invariants of .github/workflows/publish.yml
// the same way ci_test.go asserts them for ci.yml: publish.yml lives under a
// dot-prefixed directory the Go tool skips, so nothing else in this
// repository can look at it, and a plausible edit could break any of these
// silently.
package ci

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const publishWorkflowPath = "../../.github/workflows/publish.yml"

// permBlockKeys reads the `key: value` lines of a permissions: block starting
// at lines[startIdx+1], indented indent spaces, until a non-matching line
// ends the block.
func permBlockKeys(lines []string, startIdx, indent int) map[string]string {
	got := map[string]string{}
	keyLine := regexp.MustCompile(`^\s{` + strconv.Itoa(indent) + `}([\w-]+):\s*(\S+)\s*$`)
	for _, line := range lines[startIdx+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		got[m[1]] = m[2]
	}
	return got
}

// TestPublishPermissionsAreExact pins the top-level permissions block to
// contents: read only, and the write scopes to the job that actually pushes:
// packages: write to push to GHCR, id-token: write for cosign's keyless OIDC
// flow. A pull_request run must never see those two scopes.
func TestPublishPermissionsAreExact(t *testing.T) {
	raw := readWorkflow(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	topIdx := -1
	for i, line := range lines {
		if line == "permissions:" {
			topIdx = i
			break
		}
	}
	if topIdx == -1 {
		t.Fatalf("%s has no top-level (column-zero) `permissions:` block", publishWorkflowPath)
	}
	if got := permBlockKeys(lines, topIdx, 2); len(got) != 1 || got["contents"] != "read" {
		t.Errorf("%s top-level permissions = %v, want exactly {contents: read}", publishWorkflowPath, got)
	}

	prCheckIdx, publishIdx := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "pr-check:":
			prCheckIdx = i
		case "publish:":
			publishIdx = i
		}
	}
	if prCheckIdx == -1 {
		t.Fatalf("%s has no `pr-check:` job", publishWorkflowPath)
	}
	if publishIdx == -1 {
		t.Fatalf("%s has no `publish:` job", publishWorkflowPath)
	}

	jobPermIdx := func(from, to int) int {
		for i := from; i < to && i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "permissions:" {
				return i
			}
		}
		return -1
	}

	prPermIdx := jobPermIdx(prCheckIdx, publishIdx)
	if prPermIdx == -1 {
		t.Fatalf("%s: `pr-check` job has no `permissions:` block", publishWorkflowPath)
	}
	if got := permBlockKeys(lines, prPermIdx, 6); len(got) != 1 || got["contents"] != "read" {
		t.Errorf("%s pr-check job permissions = %v, want exactly {contents: read}", publishWorkflowPath, got)
	}

	pubPermIdx := jobPermIdx(publishIdx, len(lines))
	if pubPermIdx == -1 {
		t.Fatalf("%s: `publish` job has no `permissions:` block", publishWorkflowPath)
	}
	want := map[string]string{
		"contents": "read",
		"packages": "write",
		"id-token": "write",
	}
	got := permBlockKeys(lines, pubPermIdx, 6)
	if len(got) != len(want) {
		t.Fatalf("%s publish job permissions has %d keys (%v), want exactly %v", publishWorkflowPath, len(got), got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s publish job permissions.%s = %q, want %q", publishWorkflowPath, k, got[k], v)
		}
	}
}

// TestPullRequestRunsNeverPush proves the `pr-check` job — the one that runs
// on a pull_request event — can never push, sign or alias an image: none of
// those four strings appear in it, and every occurrence of any of them
// elsewhere in the file sits inside the `publish` job, which is gated on
// `github.event_name != 'pull_request'`.
func TestPullRequestRunsNeverPush(t *testing.T) {
	raw := readWorkflow(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	jobStart := regexp.MustCompile(`^  [\w-]+:\s*$`)
	jobIdx := map[string]int{}
	var jobOrder []string
	for i, line := range lines {
		if jobStart.MatchString(line) {
			name := strings.TrimSuffix(strings.TrimSpace(line), ":")
			jobIdx[name] = i
			jobOrder = append(jobOrder, name)
		}
	}
	prCheckIdx, ok := jobIdx["pr-check"]
	if !ok {
		t.Fatalf("%s has no `pr-check:` job", publishWorkflowPath)
	}
	publishIdx, ok := jobIdx["publish"]
	if !ok {
		t.Fatalf("%s has no `publish:` job", publishWorkflowPath)
	}

	jobEnd := func(start int) int {
		end := len(lines)
		for _, i := range jobIdx {
			if i > start && i < end {
				end = i
			}
		}
		return end
	}
	prCheckEnd := jobEnd(prCheckIdx)
	publishEnd := jobEnd(publishIdx)

	dangerous := []string{"docker/login-action", "push: true", "cosign sign", "imagetools create"}

	for i := prCheckIdx; i < prCheckEnd; i++ {
		for _, d := range dangerous {
			if strings.Contains(lines[i], d) {
				t.Errorf("%s: pr-check job line %d contains %q; a PR run must never push, sign, or alias an image", publishWorkflowPath, i, d)
			}
		}
	}
	if !containsAll(strings.Join(lines[prCheckIdx:prCheckEnd], "\n"), []string{"push: false"}) {
		t.Errorf("%s: pr-check job has no `push: false`", publishWorkflowPath)
	}

	publishIf := ""
	for i := publishIdx; i < publishEnd; i++ {
		if strings.Contains(lines[i], "if:") {
			publishIf = lines[i]
			break
		}
	}
	if !strings.Contains(publishIf, "github.event_name != 'pull_request'") {
		t.Errorf("%s: `publish` job's `if:` = %q, want it gated on github.event_name != 'pull_request'", publishWorkflowPath, publishIf)
	}

	for i, line := range lines {
		if i >= publishIdx && i < publishEnd {
			continue
		}
		for _, d := range dangerous {
			if strings.Contains(line, d) {
				t.Errorf("%s: line %d contains %q outside the `publish` job", publishWorkflowPath, i, d)
			}
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
	raw := readWorkflow(t, publishWorkflowPath)
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
	raw := readWorkflow(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	trivyIdx, cosignSignIdx := -1, -1
	for i, line := range lines {
		switch {
		case trivyIdx == -1 && containsAll(line, []string{"uses:", "aquasecurity/trivy-action"}):
			trivyIdx = i
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

	// The Trivy step's own with: block ends at the next step ("- uses:",
	// "- name:" or "- run:" at the same indentation as the trivy step's
	// leading "- "). exit-code: '1' must appear within that span, not merely
	// somewhere in the file, or moving it to another step would still pass.
	stepIndent := line0Indent(lines[trivyIdx])
	stepEnd := len(lines)
	for i := trivyIdx + 1; i < len(lines); i++ {
		if isStepStart(lines[i], stepIndent) {
			stepEnd = i
			break
		}
	}
	exitCodeIdx := -1
	for i := trivyIdx; i < stepEnd; i++ {
		if containsAll(lines[i], []string{"exit-code:", "'1'"}) {
			exitCodeIdx = i
			break
		}
	}

	if exitCodeIdx == -1 {
		t.Errorf("%s: trivy step (lines %d-%d) has no `exit-code: '1'`; a HIGH/CRITICAL finding would not fail the job", publishWorkflowPath, trivyIdx, stepEnd-1)
	}
	if !(trivyIdx < cosignSignIdx) {
		t.Errorf("%s: trivy step (line %d) is not before `cosign sign` (line %d); a scan failure must leave the image unsigned",
			publishWorkflowPath, trivyIdx, cosignSignIdx)
	}
}

// line0Indent returns the number of leading spaces before the `- ` that
// starts a workflow step.
func line0Indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// isStepStart reports whether line begins a new step at the given
// indentation: a new `- ` list item (uses:, name:, run:, if:, id: — whatever
// key happens to lead that step) at exactly that indent.
func isStepStart(line string, indent int) bool {
	trimmed := strings.TrimLeft(line, " ")
	return len(line)-len(trimmed) == indent && strings.HasPrefix(trimmed, "- ")
}

// cosignSignsDigest matches the cosign sign invocation and requires it to
// target an `@sha256:...`-style digest reference built from the build step's
// digest output, never a mutable `:tag`.
var cosignSignsDigest = regexp.MustCompile(`cosign sign --yes \S+@\$\{\{\s*steps\.build\.outputs\.digest\s*\}\}`)

// TestCosignSignsTheDigestNotATag proves cosign signs the immutable digest
// docker/build-push-action produced, not a tag another push could repoint.
func TestCosignSignsTheDigestNotATag(t *testing.T) {
	raw := readWorkflow(t, publishWorkflowPath)
	if !cosignSignsDigest.MatchString(raw) {
		t.Errorf("%s: no `cosign sign` step found targeting `@${{ steps.build.outputs.digest }}`", publishWorkflowPath)
	}
}

// TestLatestIsTaggedOnlyAfterSigning proves the build step never pushes the
// mutable `:latest` tag itself — a failed Trivy scan must not leave `:latest`
// pointing at an unscanned image — and that `:latest` (and any release tag)
// is only aliased onto the already-signed digest by an `imagetools create`
// step that runs after `cosign sign`.
func TestLatestIsTaggedOnlyAfterSigning(t *testing.T) {
	raw := readWorkflow(t, publishWorkflowPath)
	lines := strings.Split(raw, "\n")

	buildIdx, buildEnd := -1, -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- id: build" {
			buildIdx = i
			break
		}
	}
	if buildIdx == -1 {
		t.Fatalf("%s: no `- id: build` step found", publishWorkflowPath)
	}
	indent := line0Indent(lines[buildIdx])
	buildEnd = len(lines)
	for i := buildIdx + 1; i < len(lines); i++ {
		if isStepStart(lines[i], indent) {
			buildEnd = i
			break
		}
	}
	for i := buildIdx; i < buildEnd; i++ {
		if strings.Contains(lines[i], "latest") {
			t.Errorf("%s: build step (lines %d-%d) tags block contains `latest`; a failed scan would leave `:latest` pointing at an unscanned image",
				publishWorkflowPath, buildIdx, buildEnd-1)
			break
		}
	}

	cosignSignIdx, imagetoolsIdx := -1, -1
	for i, line := range lines {
		switch {
		case cosignSignIdx == -1 && containsAll(line, []string{"cosign sign"}):
			cosignSignIdx = i
		case imagetoolsIdx == -1 && containsAll(line, []string{"imagetools create"}):
			imagetoolsIdx = i
		}
	}
	if cosignSignIdx == -1 {
		t.Fatalf("%s: no `cosign sign` step found", publishWorkflowPath)
	}
	if imagetoolsIdx == -1 {
		t.Fatalf("%s: no `imagetools create` step found", publishWorkflowPath)
	}
	if !(imagetoolsIdx > cosignSignIdx) {
		t.Errorf("%s: `imagetools create` (line %d) is not after `cosign sign` (line %d); `:latest` would be aliased before the image is signed",
			publishWorkflowPath, imagetoolsIdx, cosignSignIdx)
	}
}

// TestShortTagWidthIsPinned asserts git rev-parse pins the short SHA width
// to 7 characters, matching the Ansible side.
func TestShortTagWidthIsPinned(t *testing.T) {
	raw := readWorkflow(t, publishWorkflowPath)
	if !strings.Contains(raw, "git rev-parse --short=7 HEAD") {
		t.Error(publishWorkflowPath + ": git rev-parse does not pin the short width to 7 characters; the CI clone and the operator's clone can drift on auto-abbrev width")
	}
}
