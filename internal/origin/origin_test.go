package origin_test

import (
	"testing"

	"airbg.org/internal/origin"
)

const site = "https://airbg.org"

// TestLoopback is the security-load-bearing table. The refused cases are not
// hypothetical: each one carries a substring that a HasPrefix, HasSuffix or
// Contains check accepts, and each names a host an attacker controls.
func TestLoopback(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    string
		want bool
	}{
		{"an ephemeral port", "http://127.0.0.1:60659", true},
		{"a different ephemeral port", "http://127.0.0.1:60662", true},
		{"elsewhere in 127.0.0.0/8", "http://127.0.0.2:8080", true},
		{"IPv6 loopback", "http://[::1]:60659", true},
		{"localhost by name", "http://localhost:3000", true},
		{"localhost with no port", "http://localhost", true},

		{"a hostname that merely starts with 127.0.0.1", "http://127.0.0.1.evil.test", false},
		{"a hostname that merely starts with localhost", "http://localhost.evil.test", false},
		{"a hostname that merely ends with localhost", "http://evil.test.localhost", false},
		{"a hostname that merely contains localhost", "http://x.localhost.evil.test", false},
		{"a public address", "http://93.184.216.34:8080", false},
		{"loopback over https", "https://127.0.0.1:60659", false},
		{"loopback with a path", "http://127.0.0.1:60659/", false},
		{"loopback with userinfo", "http://user@127.0.0.1:60659", false},
		{"the site's own origin", site, false},
		{"the empty string", "", false},
		{"the opaque origin", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := origin.Loopback(tc.o); got != tc.want {
				t.Errorf("Loopback(%q) = %v, want %v", tc.o, got, tc.want)
			}
		})
	}
}

// The two rules are independent: named without loopback, loopback without the
// named list widening, and neither one letting a stranger through.
func TestAllows(t *testing.T) {
	closed, err := origin.NewAllowlist([]string{site}, false)
	if err != nil {
		t.Fatalf("NewAllowlist error = %v, want nil", err)
	}
	open, err := origin.NewAllowlist([]string{site}, true)
	if err != nil {
		t.Fatalf("NewAllowlist error = %v, want nil", err)
	}

	for _, tc := range []struct {
		name           string
		o              string
		closed, opened bool
	}{
		{"the named origin", site, true, true},
		{"a loopback origin", "http://127.0.0.1:60659", false, true},
		{"a lookalike of loopback", "http://127.0.0.1.evil.test", false, false},
		{"a stranger", "https://evil.test", false, false},
		{"no Origin header", "", false, false},
		// The switch must not turn the byte-for-byte named list into a
		// scheme-insensitive or host-only one.
		{"the named origin over http", "http://airbg.org", false, false},
		{"a subdomain of the named origin", "https://x.airbg.org", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := closed.Allows(tc.o); got != tc.closed {
				t.Errorf("closed.Allows(%q) = %v, want %v", tc.o, got, tc.closed)
			}
			if got := open.Allows(tc.o); got != tc.opened {
				t.Errorf("open.Allows(%q) = %v, want %v", tc.o, got, tc.opened)
			}
		})
	}
}

// A constructor that accepts these produces a handler that looks configured and
// is not. Each returns an error rather than a permissive allowlist.
func TestNewAllowlistRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		named []string
	}{
		{"a nil list", nil},
		{"an empty list", []string{}},
		{"an empty origin", []string{site, ""}},
		{"the wildcard", []string{"*"}},
		{"the wildcard alongside a real origin", []string{site, "*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := origin.NewAllowlist(tc.named, false); err == nil {
				t.Error("NewAllowlist returned nil error, want an error")
			}
		})
	}
}
