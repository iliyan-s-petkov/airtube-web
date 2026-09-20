// Package deploy holds no Go code. It holds the production deployment
// artefacts, and this test, which asserts the security-relevant facts about
// them.
package deploy

import (
	"os"
	"strings"
	"testing"
)

// TestSSHIsRateLimited asserts the SSH rule contains `ct state new`,
// `limit rate` and `accept`, and that no line is `flush ruleset`.
func TestSSHIsRateLimited(t *testing.T) {
	data, err := os.ReadFile("nftables.conf")
	if err != nil {
		t.Fatalf("ReadFile(nftables.conf) error = %v, want nil", err)
	}
	content := string(data)

	// Check for the dangerous flush ruleset command (not in comments)
	for _, line := range strings.Split(content, "\n") {
		// Strip comments from the line
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "flush ruleset" {
			t.Error("nftables.conf contains `flush ruleset`, which destroys Docker's tables — delete only the table this file owns")
		}
	}

	// Find the SSH rule
	var sshLine string
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "tcp dport 22") {
			sshLine = line
			break
		}
	}

	if sshLine == "" {
		t.Fatal("nftables.conf contains no line with `tcp dport 22`")
	}

	// Verify rate limiting tokens are present
	if !strings.Contains(sshLine, "ct state new") {
		t.Error("SSH rule does not contain `ct state new` — it counts all packets, not just new connections")
	}
	if !strings.Contains(sshLine, "limit rate") {
		t.Error("SSH rule does not contain `limit rate` — rate limiting is not configured")
	}
	if !strings.Contains(sshLine, "accept") {
		t.Error("SSH rule does not contain `accept` — the rule has no verdict")
	}

	// Verify limit appears before accept (if limit comes after accept, it's dead text)
	limitIdx := strings.Index(sshLine, "limit rate")
	acceptIdx := strings.Index(sshLine, "accept")
	if limitIdx >= acceptIdx {
		t.Error("SSH rule: `limit rate` does not appear before `accept` — the limit is dead text after the verdict")
	}
}
