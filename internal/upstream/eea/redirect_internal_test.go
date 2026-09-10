package eea

import (
	"net/http"
	"net/url"
	"testing"
)

// sameOriginOnly is the http.Client CheckRedirect hook, so its scheme half is
// unreachable from an httptest server: httptest serves one scheme at a time and
// cannot produce a same-host https->http hop. Called directly instead.
func TestSameOriginOnly(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"same origin", "https://eea.example/a", "https://eea.example/b", false},
		{"other host", "https://eea.example/a", "https://evil.example/b", true},
		{"scheme downgrade", "https://eea.example/a", "http://eea.example/b", true},
		{"scheme upgrade", "http://eea.example/a", "https://eea.example/b", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first, err := url.Parse(tc.from)
			if err != nil {
				t.Fatal(err)
			}
			next, err := url.Parse(tc.to)
			if err != nil {
				t.Fatal(err)
			}
			err = sameOriginOnly(&http.Request{URL: next}, []*http.Request{{URL: first}})
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestSameOriginOnlyStopsARedirectLoop(t *testing.T) {
	first, err := url.Parse("https://eea.example/a")
	if err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = &http.Request{URL: first}
	}
	if err := sameOriginOnly(&http.Request{URL: first}, via); err == nil {
		t.Error("a same-origin chain 10 hops deep was allowed to continue")
	}
}
