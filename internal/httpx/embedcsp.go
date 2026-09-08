package httpx

import "strings"

// EmbedFrameAncestors is who may frame the embeddable map: any https origin.
// The embed carries no reader state — no cookie, no session, no form — so
// framing it is not a clickjacking target the way a logged-in page would be.
const EmbedFrameAncestors = "https:"

// EmbedCSP is csp with its frame-ancestors directive replaced, so the one route
// that is meant to be framed can be, while every other response keeps the
// operator's policy including its own frame-ancestors.
//
// The policy is taken apart into directives and reassembled rather than
// string-replaced: a substring edit that gets the boundaries wrong widens a
// directive nobody meant to touch, and this input is an operator-supplied
// string of unknown shape.
func EmbedCSP(csp string) string {
	if csp == "" {
		csp = CSPValue
	}
	directives := strings.Split(csp, ";")
	out := make([]string, 0, len(directives)+1)
	for _, directive := range directives {
		directive = strings.TrimSpace(directive)
		if directive == "" {
			continue
		}
		name, _, _ := strings.Cut(directive, " ")
		if strings.EqualFold(name, "frame-ancestors") {
			continue
		}
		out = append(out, directive)
	}
	return strings.Join(append(out, "frame-ancestors "+EmbedFrameAncestors), "; ")
}
