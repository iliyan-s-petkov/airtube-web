// Package ci holds no Go code. It holds this test, which asserts the facts
// about .github/workflows/ci.yml that a plausible edit could break silently.
//
// The workflow lives under a dot-prefixed directory, which the Go tool skips
// when expanding ./... — so nothing else in this repository can look at it.
// That invisibility is the reason to assert on it here rather than trust a
// comment in the YAML: a comment saying "never @latest" is documentation, and
// documentation does not fail a build.
package ci

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const workflowPath = "../../.github/workflows/ci.yml"

// goRunTool matches a `go run <module path>/cmd/<tool>@<version>` invocation
// and captures the module path and the version separately, so the test can
// report which tool is unpinned rather than just that something is.
//
// The version group is deliberately permissive (everything up to whitespace):
// matching only well-formed semver here would make `@latest` fail to match at
// all, and an unpinned step would then pass by being unrecognised — the exact
// silent-zero failure this file exists to prevent.
var goRunTool = regexp.MustCompile(`go run ([\w./-]+)@(\S+)`)

// pinnedVersion is what an acceptable version looks like: an exact semver tag.
// A branch name, a commit-less "latest", or a bare major like "v1" all fail.
var pinnedVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-\S+)?$`)

// wantToolSteps is the number of `go run tool@version` steps ci.yml currently
// has: three staticcheck runs (untagged, integration, e2e) and three
// govulncheck runs of the same shape.
//
// This is the positive control, and it is the assertion that makes the rest of
// the test mean anything. Without it, deleting the analyser steps, renaming the
// workflow file's key structure, or rewriting the invocations into a form the
// regexp does not recognise would leave the scan matching nothing at all — and
// a loop over zero matches raises zero failures. "No unpinned tools found"
// reads identically whether the tools are pinned or absent.
const wantToolSteps = 6

func readWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	return string(raw)
}

// TestAnalysersArePinnedToExactVersions. `@latest` resolves and then EXECUTES
// whatever the module proxy serves at that moment, inside a job that has this
// repository checked out. go.sum does not cover it: these analysers are run as
// tools, not required as module dependencies, so nothing records a checksum for
// them. It also makes a run unreproducible — the same commit can be green today
// and red tomorrow because upstream added a check, which teaches people to
// re-run CI rather than read it.
func TestAnalysersArePinnedToExactVersions(t *testing.T) {
	matches := goRunTool.FindAllStringSubmatch(readWorkflow(t), -1)

	if len(matches) != wantToolSteps {
		t.Fatalf("found %d `go run <tool>@<version>` steps in %s, want %d; "+
			"if a step was added or removed deliberately, update wantToolSteps — "+
			"but a count that dropped to 0 means this test is scanning nothing",
			len(matches), workflowPath, wantToolSteps)
	}

	for _, m := range matches {
		tool, version := m[1], m[2]
		if !pinnedVersion.MatchString(version) {
			t.Errorf("%s is invoked at @%s; pin it to an exact version (vX.Y.Z) and bump it deliberately", tool, version)
		}
	}
}

// TestTaggedAnalyserRunsSurvive pins the coverage that build tags would
// otherwise hide. Files behind `//go:build integration` or `//go:build e2e` are
// excluded from go vet, staticcheck and govulncheck alike, so each analyser
// needs a run per tag set or the tagged test files are never examined at all.
//
// govulncheck additionally needs -test, which defaults to FALSE: without it the
// tool opens no _test.go file whatsoever, and -tags on its own is an inert flag
// that looks like coverage. That inertness is invisible in a green log, which
// is why it is asserted rather than commented.
func TestTaggedAnalyserRunsSurvive(t *testing.T) {
	lines := strings.Split(readWorkflow(t), "\n")

	// Matched by substring rather than by exact command so that bumping a
	// pinned version does not break this test — the version is
	// TestAnalysersArePinnedToExactVersions' business, not this one's.
	for _, want := range []struct {
		name  string
		parts []string
	}{
		{"go vet, integration", []string{"go vet", "-tags integration"}},
		{"go vet, e2e", []string{"go vet", "-tags e2e"}},
		{"staticcheck, integration", []string{"staticcheck", "-tags integration"}},
		{"staticcheck, e2e", []string{"staticcheck", "-tags e2e"}},
		{"govulncheck, integration", []string{"govulncheck", "-test", "-tags integration"}},
		{"govulncheck, e2e", []string{"govulncheck", "-test", "-tags e2e"}},
	} {
		found := false
		for _, line := range lines {
			if containsAll(line, want.parts) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s has no step running %s (%v); tagged files would go unanalysed",
				workflowPath, want.name, want.parts)
		}
	}
}

// wantContractSteps is the positive control for
// TestContractIsRegeneratedAndDiffed, for the same reason wantToolSteps is
// one: a scan that matches zero steps would pass identically whether the
// contract check is missing or merely unrecognised.
const wantContractSteps = 2

// TestContractIsRegeneratedAndDiffed pins the CI half of the contract that
// stops web/src/lib/contract.json from going stale the way BBOX_QUANTUM_DEG
// did: a run of `go run ./cmd/airbg contract` and a `git diff --exit-code`
// against the committed file, the build regenerated it, both present and the
// diff step reachable after the build.
func TestContractIsRegeneratedAndDiffed(t *testing.T) {
	raw := readWorkflow(t)
	lines := strings.Split(raw, "\n")

	regenIdx, diffIdx, remedyIdx, buildIdx, vetIdx := -1, -1, -1, -1, -1
	for i, line := range lines {
		switch {
		// The remediation message names the same command, so it must be
		// recognised before the step match and the step match must keep its
		// first hit — otherwise the echo is read as the regenerate step.
		case containsAll(line, []string{"::error::", "go run ./cmd/airbg contract"}):
			remedyIdx = i
		case regenIdx == -1 && containsAll(line, []string{"go run ./cmd/airbg contract"}):
			regenIdx = i
		case containsAll(line, []string{"git diff", "--exit-code", "contract.json"}):
			diffIdx = i
		case buildIdx == -1 && containsAll(line, []string{"run: go build ./..."}):
			buildIdx = i
		case vetIdx == -1 && containsAll(line, []string{"run: go vet ./..."}):
			vetIdx = i
		}
	}

	found := 0
	if regenIdx >= 0 {
		found++
	}
	if diffIdx >= 0 {
		found++
	}
	if found != wantContractSteps {
		t.Fatalf("found %d of %d contract-check steps in %s (regen=%v diff=%v); "+
			"if a step was added or removed deliberately, update wantContractSteps",
			found, wantContractSteps, workflowPath, regenIdx >= 0, diffIdx >= 0)
	}

	// Without this the job aborts on a bare unified diff of a generated file,
	// and the message that says how to fix it lives in a `go test` step the
	// abort never reaches.
	if remedyIdx == -1 || remedyIdx < diffIdx {
		t.Errorf("the contract diff step in %s does not print the regenerate command in its own failure path "+
			"(remedy=%d diff=%d); a developer sees an unexplained diff and reverts the file instead of regenerating it",
			workflowPath, remedyIdx, diffIdx)
	}

	if buildIdx == -1 || vetIdx == -1 {
		t.Fatalf("could not locate the `go build ./...` / `go vet ./...` steps in %s to check ordering against", workflowPath)
	}
	if !(buildIdx < regenIdx && regenIdx < diffIdx && diffIdx < vetIdx) {
		t.Errorf("contract regenerate+diff steps are out of order in %s: want go build (%d) < regenerate (%d) < diff (%d) < go vet (%d); "+
			"regenerating before the build compiles cmd/airbg would run a stale binary, and diffing after go vet would let an unrelated failure mask a stale contract",
			workflowPath, buildIdx, regenIdx, diffIdx, vetIdx)
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
