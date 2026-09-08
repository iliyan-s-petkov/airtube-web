package httpx_test

import (
	"strings"
	"testing"

	"airbg.org/internal/httpx"
)

// The embed policy is the site's policy with one directive changed. Everything
// else surviving is the point: a widened frame-ancestors must not come with a
// widened script-src.
func TestEmbedCSPKeepsEveryOtherDirective(t *testing.T) {
	got := httpx.EmbedCSP(httpx.CSPValue)

	for _, directive := range strings.Split(httpx.CSPValue, ";") {
		directive = strings.TrimSpace(directive)
		if directive == "" || strings.HasPrefix(directive, "frame-ancestors") {
			continue
		}
		if !strings.Contains(got, directive) {
			t.Errorf("EmbedCSP dropped %q\ngot: %s", directive, got)
		}
	}
	if strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("EmbedCSP kept the refusal it exists to lift: %s", got)
	}
	if !strings.HasSuffix(got, "frame-ancestors "+httpx.EmbedFrameAncestors) {
		t.Errorf("EmbedCSP = %q, want it to end in frame-ancestors %s", got, httpx.EmbedFrameAncestors)
	}
}

// One frame-ancestors, whatever the operator wrote — two is a policy whose
// meaning depends on which the browser reads first.
func TestEmbedCSPReplacesRatherThanAppends(t *testing.T) {
	for _, csp := range []string{
		"default-src 'self'; frame-ancestors 'none'",
		"default-src 'self'; FRAME-ANCESTORS 'self'",
		"frame-ancestors 'none'; default-src 'self'",
		"default-src 'self'",
	} {
		got := httpx.EmbedCSP(csp)
		if n := strings.Count(strings.ToLower(got), "frame-ancestors"); n != 1 {
			t.Errorf("EmbedCSP(%q) = %q has %d frame-ancestors directives, want 1", csp, got, n)
		}
		if !strings.Contains(got, "default-src 'self'") {
			t.Errorf("EmbedCSP(%q) = %q lost default-src", csp, got)
		}
	}
}

// Empty means a caller forgot the argument, which must not yield a policy of
// nothing but frame-ancestors.
func TestEmbedCSPFallsBackToTheBaseline(t *testing.T) {
	got := httpx.EmbedCSP("")
	if !strings.Contains(got, "script-src 'self'") || !strings.Contains(got, "object-src 'none'") {
		t.Errorf("EmbedCSP(\"\") = %q, want the CSPValue baseline widened", got)
	}
}

// A wildcard here would let any scheme frame it, http: included, which is a
// mixed-content downgrade of the whole embed.
func TestEmbedFrameAncestorsIsHTTPSOnly(t *testing.T) {
	if httpx.EmbedFrameAncestors != "https:" {
		t.Errorf("EmbedFrameAncestors = %q, want https:", httpx.EmbedFrameAncestors)
	}
}
