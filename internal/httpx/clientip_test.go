package httpx_test

import (
	"net/http"
	"net/http/httptest"
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
