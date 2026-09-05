// Package origin decides which browser Origin values may read a resource
// cross-origin.
//
// It exists as its own package, with no dependencies, because two unrelated
// surfaces need the same decision: the tiles listener, which deliberately
// imports nothing, and the JSON API's middleware. A second copy of the matcher
// is a second place for the loopback rule to be got subtly wrong, and the way
// it goes wrong — a prefix match that accepts a hostname merely beginning with
// 127.0.0.1 — is invisible until someone attacks it.
package origin

import (
	"errors"
	"net"
	"net/url"
)

// Allowlist answers whether one Origin header value may read a resource.
//
// Named origins and loopback are separate rules on purpose. The named list is
// compared byte for byte, so a wildcard entry in it would match nothing while
// looking like it should — the silent failure config validation refuses. A
// preview host's port is ephemeral and so cannot be named at all, which is what
// the second rule is for.
type Allowlist struct {
	named    map[string]bool
	loopback bool
}

// NewAllowlist requires at least one named origin. Loopback alone is not a
// configuration worth expressing: the surface's own origin is always named, and
// a resource readable only from someone's laptop is not one worth serving.
func NewAllowlist(named []string, allowLoopback bool) (*Allowlist, error) {
	if len(named) == 0 {
		// Not defaulted to "*": that would let any page on the internet read
		// the resource, and the values we want are known at startup.
		return nil, errors.New("origin: named is empty")
	}
	m := make(map[string]bool, len(named))
	for _, o := range named {
		if o == "" {
			return nil, errors.New("origin: named contains an empty origin")
		}
		// Refused here as well as in config validation, because this
		// constructor is the last thing between a caller and the wildcard —
		// and a wildcard is not a wider allowlist, it is no allowlist.
		if o == "*" {
			return nil, errors.New(`origin: named contains "*"; name each origin`)
		}
		m[o] = true
	}
	return &Allowlist{named: m, loopback: allowLoopback}, nil
}

// Allows reports whether o may read cross-origin. The empty string is a request
// with no Origin header, which is not a cross-origin browser read and gets no
// header back.
func (a *Allowlist) Allows(o string) bool {
	if o == "" {
		return false
	}
	if a.named[o] {
		return true
	}
	return a.loopback && Loopback(o)
}

// Loopback reports whether o is a plain http origin on the requesting machine.
//
// Parsed, never prefix-matched. "http://127.0.0.1.evil.test" and
// "http://evil.test.localhost" both carry the string a Contains or HasPrefix
// check would accept, and neither is loopback; net.ParseIP on the hostname is
// what tells 127.0.0.1 apart from a name that merely begins with it. IsLoopback
// covers all of 127.0.0.0/8 and ::1 rather than the one address people happen
// to type.
//
// "localhost" is accepted by name because it is the other host a local preview
// server binds, and it is the one name a browser is required to treat as
// loopback — it cannot be pointed elsewhere by DNS the way an ordinary name can.
//
// https is refused: a loopback origin over TLS is not something a preview server
// produces, and accepting it would widen the rule to hosts reachable through a
// proxy that terminates TLS on their behalf.
func Loopback(o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
