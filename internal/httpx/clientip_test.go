package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"airbg.org/internal/httpx"
)

func resolver(t *testing.T) *httpx.IPResolver {
	t.Helper()
	r, err := httpx.NewIPResolver(httpx.DefaultCloudflareCIDRs())
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	return r
}

// TestTrustedPeerHeaderIsHonoured — the happy path.
func TestTrustedPeerHeaderIsHonoured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// 173.245.48.0/20 is a published Cloudflare range.
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	if got := resolver(t).ClientIP(req).String(); got != "198.51.100.7" {
		t.Errorf("ClientIP = %s, want 198.51.100.7", got)
	}
}

// TestUntrustedPeerHeaderIsIgnored is the test that matters. Without it, an
// implementation that trusts the header unconditionally passes every other test
// in this file — and every rate limit in the system becomes a no-op, because a
// scraper simply sets a fresh CF-Connecting-IP per request.
func TestUntrustedPeerHeaderIsIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:41000" // not Cloudflare
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	got := resolver(t).ClientIP(req).String()
	if got == "198.51.100.7" {
		t.Fatal("CF-Connecting-IP from a non-Cloudflare peer was trusted; every rate limit is now spoofable")
	}
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %s, want the socket address 203.0.113.9", got)
	}
}

// TestSpoofedHeaderChainIsIgnored: multiple comma-separated values, the classic
// X-Forwarded-For prepend attack shape, arriving from an untrusted peer.
func TestSpoofedHeaderChainIsIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:41000"
	req.Header.Set("CF-Connecting-IP", "1.2.3.4, 5.6.7.8")
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	if got := resolver(t).ClientIP(req).String(); got != "203.0.113.9" {
		t.Errorf("ClientIP = %s, want 203.0.113.9; no forwarded header may be read from an untrusted peer", got)
	}
}

// TestCommaBearingHeaderFromTrustedPeerFallsBackToPeer pins the comma reject in
// ClientIP — the one place where a mistake is a full client-IP spoof.
//
// Cloudflare sends exactly ONE address in CF-Connecting-IP, never a list. A
// comma therefore means the value did not come from Cloudflare, so it must be
// rejected outright and the socket peer used instead. The tempting "lenient"
// refactor — split on comma and take the first (or last) element — is the
// classic X-Forwarded-For prepend attack: a client behind the proxy chooses
// which address it is attributed to and every rate limit in the system becomes
// a no-op. That refactor is exactly what this test forbids.
//
// Note on the trusted peer: this is the dangerous direction. The untrusted-peer
// case (TestSpoofedHeaderChainIsIgnored) is guarded by the trust check one line
// earlier; here the trust check PASSES and the comma reject is the only thing
// standing between the caller and its chosen identity.
func TestCommaBearingHeaderFromTrustedPeerFallsBackToPeer(t *testing.T) {
	// Both header elements are individually well-formed addresses, so a
	// split-and-parse implementation would happily accept either one.
	const (
		peer     = "173.245.48.1" // inside 173.245.48.0/20, a real Cloudflare range
		first    = "1.2.3.4"
		appended = "9.9.9.9"
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer + ":41000"
	req.Header.Set("CF-Connecting-IP", first+", "+appended)

	// Name the address that was chosen, so a failure says WHO was trusted
	// rather than merely that the answer was wrong.
	switch got := resolver(t).ClientIP(req).String(); got {
	case peer:
		// Correct: the comma-bearing header was rejected entirely.
	case first:
		t.Fatalf("ClientIP = %s, want the socket peer %s: the FIRST element of a "+
			"comma-separated CF-Connecting-IP was trusted, so a client behind the "+
			"proxy picks its own rate-limit identity by prepending an address", got, peer)
	case appended:
		t.Fatalf("ClientIP = %s, want the socket peer %s: the LAST element of a "+
			"comma-separated CF-Connecting-IP was trusted, so a client behind the "+
			"proxy picks its own rate-limit identity by appending an address", got, peer)
	default:
		t.Fatalf("ClientIP = %s, want the socket peer %s: a comma-bearing "+
			"CF-Connecting-IP must be rejected and the peer used", got, peer)
	}
}

// TestZonedCommaHeaderFromTrustedPeerFallsBackToPeer is the case that proves
// `!strings.Contains(v, ",")` is load-bearing rather than decorative, and it is
// here because a previous round asserted the opposite and was wrong.
//
// The claim was "netip.ParseAddr rejects every string containing a comma, so
// deleting the comma check cannot change behaviour". That is true of every
// dotted-quad and plain IPv6 form — and false for a ZONE IDENTIFIER. Everything
// after `%` in an IPv6 address is an opaque interface name, so `fe80::1%a,b`
// parses cleanly, commas and all. With the comma check deleted this value is
// accepted, and BucketKey then takes its /64:
//
//	guard present → ClientIP 173.245.48.1, bucket "173.245.48.1"
//	guard deleted → ClientIP fe80::1%a,b,  bucket "fe80::/64"
//
// The exploit is not a parse error, it is bucket selection. A caller behind the
// trusted proxy names an arbitrary /64 and so chooses which bucket it spends:
// a fresh one per request to evade the limiter entirely, or a victim's to
// exhaust someone else's allowance. Both directions are handed over by one
// deleted conjunct.
//
// The bucket key is asserted as well as the address, because the key is the
// thing the attack manipulates — an address assertion alone would pass any
// future change that resolved the peer correctly but keyed on something else.
func TestZonedCommaHeaderFromTrustedPeerFallsBackToPeer(t *testing.T) {
	const (
		peer = "173.245.48.1" // inside 173.245.48.0/20, a real Cloudflare range
		// A comma-bearing value that netip.ParseAddr ACCEPTS: the comma sits in
		// the zone, which is an opaque string.
		zoned      = "fe80::1%a,b"
		zonedBuckt = "fe80::/64"
	)

	// Guard the premise. If a future Go release tightens zone parsing, this
	// stops being the case that pins the comma check, and the test should say
	// so loudly rather than pass for the wrong reason.
	if _, err := netip.ParseAddr(zoned); err != nil {
		t.Fatalf("premise broken: netip.ParseAddr(%q) now fails (%v), so this test no "+
			"longer exercises an accepted comma-bearing value; find another one or the "+
			"comma check is genuinely unobservable", zoned, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer + ":41000"
	req.Header.Set("CF-Connecting-IP", zoned)

	res := resolver(t)

	switch got := res.ClientIP(req).String(); got {
	case peer:
		// Correct: rejected on the comma, before parsing could succeed.
	case zoned:
		t.Errorf("ClientIP = %s, want the socket peer %s: a comma-bearing "+
			"CF-Connecting-IP was accepted because its comma hid in an IPv6 zone, so the "+
			"caller now names its own rate-limit bucket", got, peer)
	default:
		t.Errorf("ClientIP = %s, want the socket peer %s", got, peer)
	}

	switch got := res.BucketKey(req); got {
	case peer:
		// Correct: keyed on the socket peer.
	case zonedBuckt:
		t.Errorf("BucketKey = %q, want %q: the caller chose its own /64 bucket through a "+
			"zoned CF-Connecting-IP — it can mint a fresh bucket per request to evade the "+
			"limiter, or name a victim's /64 to exhaust their allowance", got, peer)
	default:
		t.Errorf("BucketKey = %q, want %q", got, peer)
	}
}

// TestMalformedHeaderFromTrustedPeerFallsBack: a trusted peer sending garbage
// must fall back to the socket address, not produce a zero Addr. A zero Addr
// stringifies to "invalid IP" and every such request would share one bucket —
// so one malformed header would let an attacker pool all their traffic into a
// single key, or grief every other client sharing it.
func TestMalformedHeaderFromTrustedPeerFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-Connecting-IP", "not-an-ip")

	got := resolver(t).ClientIP(req)
	if !got.IsValid() {
		t.Fatal("ClientIP returned an invalid Addr; a malformed header must fall back to the socket address")
	}
	if got.String() != "173.245.48.1" {
		t.Errorf("ClientIP = %s, want 173.245.48.1", got)
	}
}

// TestBucketKeyGroupsIPv6By64 is the IPv6 defeat, asserted deliberately.
//
// A single IPv6 host is routinely allocated a /64 — 2^64 addresses. Keying a
// rate limit on the full address against such a client is not rate limiting at
// all: it rotates source addresses at zero cost and never hits the same bucket
// twice. The failure is invisible when testing over IPv4, which is why this test
// exists rather than being left to integration.
func TestBucketKeyGroupsIPv6By64(t *testing.T) {
	res := resolver(t)

	keys := map[string]bool{}
	for _, addr := range []string{
		"[2001:db8:abcd:0012::1]:41000",
		"[2001:db8:abcd:0012::2]:41000",
		"[2001:db8:abcd:0012:ffff:ffff:ffff:ffff]:41000",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		keys[res.BucketKey(req)] = true
	}
	if len(keys) != 1 {
		t.Errorf("three addresses in one /64 produced %d bucket keys, want 1: %v", len(keys), keys)
	}

	// A different /64 must be a different bucket, or the limiter would lump
	// unrelated clients together and one abuser would 429 the neighbourhood.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8:abcd:0013::1]:41000"
	if k := res.BucketKey(req); keys[k] {
		t.Error("a different /64 shares a bucket key with the first")
	}
}

// TestBucketKeyUsesFullIPv4Address: /64 grouping must not leak into IPv4, where
// a /24 would sweep up 256 unrelated customers.
func TestBucketKeyUsesFullIPv4Address(t *testing.T) {
	res := resolver(t)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "203.0.113.1:41000"
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.2:41000"

	if res.BucketKey(req1) == res.BucketKey(req2) {
		t.Error("two distinct IPv4 addresses share a bucket key")
	}
}

// TestIPv4MappedIPv6IsNormalised: a dual-stack listener reports IPv4 peers as
// ::ffff:203.0.113.1. Left unnormalised, the same client gets one bucket over
// IPv4 and a second, /64-grouped one over the mapped form — and the mapped /64
// covers the entire IPv4 space, so every IPv4 client would share one bucket.
func TestIPv4MappedIPv6IsNormalised(t *testing.T) {
	res := resolver(t)

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	plain.RemoteAddr = "203.0.113.1:41000"
	mapped := httptest.NewRequest(http.MethodGet, "/", nil)
	mapped.RemoteAddr = "[::ffff:203.0.113.1]:41000"

	if a, b := res.BucketKey(plain), res.BucketKey(mapped); a != b {
		t.Errorf("bucket keys differ for the same client: %q vs %q", a, b)
	}
}

// TestEmptyTrustedListTrustsNothing: the no-proxy deployment mode. With no
// trusted CIDRs, no header may ever be honoured.
func TestEmptyTrustedListTrustsNothing(t *testing.T) {
	res, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver(nil): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.1:41000" // a Cloudflare address
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	if got := res.ClientIP(req).String(); got != "173.245.48.1" {
		t.Errorf("ClientIP = %s, want 173.245.48.1; an empty trusted list must trust no header, not fall back to the defaults", got)
	}
}

func TestNewIPResolverRejectsMalformedCIDR(t *testing.T) {
	if _, err := httpx.NewIPResolver([]string{"not-a-cidr"}); err == nil {
		t.Fatal("NewIPResolver accepted a malformed CIDR; a typo in AIRBG_TRUSTED_PROXY_CIDRS must fail at startup, not silently trust nothing at runtime")
	}
}
