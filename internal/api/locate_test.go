package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
)

// locateVia serves a request through a resolver that trusts the given CIDRs, so
// a test can control whether the Cloudflare headers are honoured.
func locateVia(t *testing.T, d api.Deps, trusted []string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	res, err := httpx.NewIPResolver(trusted)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	h := httpx.WithClientIP(api.NewRouter(d), res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type locateResponse struct {
	Slug   string  `json:"slug"`
	Name   string  `json:"name"`
	Lon    float64 `json:"lon"`
	Lat    float64 `json:"lat"`
	Zoom   int     `json:"zoom"`
	Source string  `json:"source"`
}

func TestLocateUsesCloudflareHeadersFromATrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-IPLatitude", "42.6977")
	req.Header.Set("CF-IPLongitude", "23.3219")

	rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Slug != "sofia" {
		t.Errorf("slug = %q, want sofia", got.Slug)
	}
	if got.Source != "geoip" {
		t.Errorf("source = %q, want geoip", got.Source)
	}
	if got.Zoom == 0 {
		t.Error("zoom is 0; the client has no initial view")
	}
}

// TestLocateIgnoresHeadersFromAnUntrustedPeer. Otherwise anyone can claim any
// location — harmless for a map view on its own, but it is the same header-trust
// bug as the client IP, and letting it stand here means the codebase contains a
// worked example of trusting an unverified header.
func TestLocateIgnoresHeadersFromAnUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"
	req.Header.Set("CF-IPLatitude", "42.6977")
	req.Header.Set("CF-IPLongitude", "23.3219")

	rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "default" {
		t.Errorf("source = %q, want default; the headers came from an untrusted peer", got.Source)
	}
}

// TestLocateFallsBackToTheNationalView: no headers at all — the local
// development case, and any deployment without Cloudflare. It must still return
// a usable view, because the frontend has nothing else to open with.
func TestLocateFallsBackToTheNationalView(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"

	rec := locateVia(t, deps(t, fixture(t)), nil, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "default" {
		t.Errorf("source = %q, want default", got.Source)
	}
	// Bulgaria's centre, roughly. Asserted as separate ranges so a lon/lat swap
	// is visible: (25, 42) is Bulgaria, (42, 25) is Saudi Arabia.
	if got.Lon < 22 || got.Lon > 29 {
		t.Errorf("lon = %v, want a Bulgarian longitude (22–29)", got.Lon)
	}
	if got.Lat < 41 || got.Lat > 45 {
		t.Errorf("lat = %v, want a Bulgarian latitude (41–45)", got.Lat)
	}
}

// TestLocateRejectsOutOfRangeHeaderValues: a trusted peer can still send
// nonsense. Latitude 999 fed to ST_MakePoint is not an error in PostGIS — it is
// a point nothing contains — so validating here is what keeps the fallback
// honest instead of silently querying garbage.
func TestLocateRejectsOutOfRangeHeaderValues(t *testing.T) {
	for _, c := range []struct{ lat, lon string }{
		{"999", "23.3"}, {"42.7", "999"}, {"nan", "23.3"}, {"", "23.3"}, {"42.7", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
		req.RemoteAddr = "173.245.48.1:41000"
		req.Header.Set("CF-IPLatitude", c.lat)
		req.Header.Set("CF-IPLongitude", c.lon)

		rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)
		if rec.Code != http.StatusOK {
			t.Errorf("lat=%q lon=%q: status = %d, want 200 with the default view", c.lat, c.lon, rec.Code)
			continue
		}
		var got locateResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Source != "default" {
			t.Errorf("lat=%q lon=%q: source = %q, want default", c.lat, c.lon, got.Source)
		}
	}
}

// TestLocateIsNeverCachedPublicly. The response depends on the caller's IP; a
// shared cache storing it would hand one visitor's city to everyone behind the
// same edge node.
func TestLocateIsNeverCachedPublicly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"

	rec := locateVia(t, deps(t, fixture(t)), nil, req)
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "private") && !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q; a per-IP response must not be publicly cacheable", cc)
	}
}
