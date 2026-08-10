// Package httpx holds the middleware every request passes through. It knows
// nothing about handlers or the database — it operates on http.Handler alone,
// so the whole chain is testable with a stub and no container.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// IPResolver derives the client IP that rate limiting keys on.
type IPResolver struct {
	trusted []netip.Prefix
}

// NewIPResolver parses the trusted proxy CIDRs. An empty or nil list means no
// forwarded header is ever honoured — the correct behaviour for a
// directly-exposed origin.
//
// A malformed CIDR is a startup error, not a warning. The alternative is a
// typo in AIRBG_TRUSTED_PROXY_CIDRS silently emptying the trusted list, at
// which point every request behind Cloudflare is attributed to a Cloudflare
// edge address — a handful of buckets shared by the entire internet, which
// rate-limits all legitimate visitors and no attacker.
func NewIPResolver(trustedCIDRs []string) (*IPResolver, error) {
	r := &IPResolver{}
	for _, c := range trustedCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("httpx: trusted proxy CIDR %q is not valid: %w", c, err)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// peerAddr returns the normalised socket peer address.
//
// Unmap is essential: a dual-stack listener reports an IPv4 peer as
// ::ffff:203.0.113.1. Left mapped, BucketKey would take its /64 — and the
// v4-mapped /64 contains the entire IPv4 address space, so every IPv4 client on
// earth would share one bucket.
func peerAddr(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// TrustsPeer reports whether the socket peer is inside a trusted proxy range.
func (r *IPResolver) TrustsPeer(req *http.Request) bool {
	addr := peerAddr(req)
	if !addr.IsValid() {
		return false
	}
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the address to attribute this request to.
//
// CF-Connecting-IP is read ONLY when the socket peer is itself a trusted proxy.
// From anyone else the header is ignored entirely — it is caller-supplied data,
// and a limiter keyed on caller-supplied data is not a limiter. This is the same
// rule the whole project applies: a safety mechanism must not sit downstream of
// the failure it guards.
//
// Cloudflare sends exactly one address in CF-Connecting-IP, never a list. A
// comma therefore means the value did not come from Cloudflare, so it is
// rejected rather than split — splitting would accept the shape of the classic
// X-Forwarded-For prepend attack.
func (r *IPResolver) ClientIP(req *http.Request) netip.Addr {
	peer := peerAddr(req)

	if r.TrustsPeer(req) {
		if v := strings.TrimSpace(req.Header.Get("CF-Connecting-IP")); v != "" && !strings.Contains(v, ",") {
			if addr, err := netip.ParseAddr(v); err == nil {
				return addr.Unmap()
			}
		}
	}
	return peer
}

// BucketKey returns the rate-limiting key for this request.
//
// IPv6 is keyed on the /64 prefix, not the address. A single IPv6 host is
// routinely allocated a /64 — 2^64 addresses — so per-address limiting against
// an IPv6 client is not rate limiting: the client rotates source addresses for
// free and never touches the same bucket twice. IPv4 keeps the full address,
// because an IPv4 prefix would sweep up unrelated customers, and Bulgarian
// mobile networks use CGNAT where one address already fronts thousands of
// legitimate users.
func (r *IPResolver) BucketKey(req *http.Request) string {
	addr := r.ClientIP(req)
	if !addr.IsValid() {
		// Unparseable peer: one shared key. Rare, and preferable to an empty
		// key that would silently exempt the request from every limit.
		return "invalid"
	}
	if addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return p.String()
}

type ctxKey int

const (
	ctxClientIP ctxKey = iota
	ctxBucketKey
	ctxPeerTrusted
)

// WithClientIP resolves the client IP once and puts it, its bucket key, and the
// peer-trust verdict in the request context.
//
// Resolving once matters: downstream middleware and handlers must all agree on
// the attribution, and re-deriving it per consumer is how a limiter and its log
// line end up naming different clients.
func WithClientIP(next http.Handler, r *IPResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx = context.WithValue(ctx, ctxClientIP, r.ClientIP(req))
		ctx = context.WithValue(ctx, ctxBucketKey, r.BucketKey(req))
		ctx = context.WithValue(ctx, ctxPeerTrusted, r.TrustsPeer(req))
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func ClientIPFrom(ctx context.Context) netip.Addr {
	addr, _ := ctx.Value(ctxClientIP).(netip.Addr)
	return addr
}

func BucketKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(ctxBucketKey).(string)
	if key == "" {
		// A handler reached without WithClientIP in front of it. Returning a
		// shared key rather than "" keeps such a request limited rather than
		// exempt — fail closed.
		return "unattributed"
	}
	return key
}

// PeerTrustedFrom reports whether the request arrived through a trusted proxy.
// /locate uses it to decide whether Cloudflare's visitor-location headers may
// be read.
func PeerTrustedFrom(ctx context.Context) bool {
	trusted, _ := ctx.Value(ctxPeerTrusted).(bool)
	return trusted
}
