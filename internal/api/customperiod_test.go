package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/store"
)

func customURL(from, to time.Time) string {
	return "/api/v1/sensor/42/series?metric=P2&period=custom" +
		"&from=" + url.QueryEscape(from.Format(time.RFC3339)) +
		"&to=" + url.QueryEscape(to.Format(time.RFC3339))
}

func customDeps(t *testing.T) (api.Deps, *stubSource) {
	t.Helper()
	src := &stubSource{slug: "sofia", points: samplePoints()}
	d := deps(t, fixture(t))
	d.Store = src
	return d, src
}

// Both ends reach the query; without the upper bound the body still looks fine.
func TestCustomPeriodBoundsBothEndsOfTheWindow(t *testing.T) {
	d, src := customDeps(t)
	from := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Second)
	to := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	rec := serve(t, d, get(customURL(from, to), "203.0.113.60"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !src.lastSince.Equal(from) {
		t.Errorf("since = %s, want %s", src.lastSince, from)
	}
	if src.lastUntil == nil {
		t.Fatal("until = nil: the query ran to the newest reading, not to the requested end of the range")
	}
	if !src.lastUntil.Equal(to) {
		t.Errorf("until = %s, want %s", src.lastUntil, to)
	}
}

// A named period runs to now, so it carries no upper bound.
func TestNamedPeriodStillHasNoUpperBound(t *testing.T) {
	d, src := customDeps(t)

	serve(t, d, get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.61"))
	if src.lastUntil != nil {
		t.Errorf("until = %s, want nil for a named period", src.lastUntil)
	}
}

// Windows the API must not run, including the span cap that stops "custom"
// asking for more than a named period can.
func TestCustomPeriodRefusesUnusableWindows(t *testing.T) {
	now := time.Now().UTC()
	cases := map[string]string{
		"no bounds at all":   "/api/v1/sensor/42/series?metric=P2&period=custom",
		"unparseable from":   "/api/v1/sensor/42/series?metric=P2&period=custom&from=yesterday&to=" + url.QueryEscape(now.Format(time.RFC3339)),
		"naive local time":   "/api/v1/sensor/42/series?metric=P2&period=custom&from=2026-09-07T14:00&to=" + url.QueryEscape(now.Format(time.RFC3339)),
		"backwards range":    customURL(now.Add(-time.Hour), now.Add(-2*time.Hour)),
		"empty range":        customURL(now.Add(-time.Hour), now.Add(-time.Hour)),
		"longer than a year": customURL(now.Add(-3*8760*time.Hour), now),
	}
	for name, path := range cases {
		d, src := customDeps(t)
		rec := serve(t, d, get(path, "203.0.113.62"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body: %s)", name, rec.Code, rec.Body.String())
		}
		if !src.lastSince.IsZero() {
			t.Errorf("%s: the refused request still reached the database", name)
		}
	}
}

// A range dragged past now is clamped, not refused.
func TestCustomPeriodClampsTheFutureRatherThanRefusingIt(t *testing.T) {
	d, src := customDeps(t)
	before := time.Now().UTC()

	rec := serve(t, d, get(customURL(before.Add(-time.Hour), before.Add(48*time.Hour)), "203.0.113.63"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if src.lastUntil == nil || src.lastUntil.After(time.Now().UTC()) {
		t.Errorf("until = %v, want it clamped to now", src.lastUntil)
	}
}

// The resolution is the configured one, which is what stops a 6-month range
// being asked of a table that keeps 30 days.
func TestCustomSpanTakesTheResolutionOfTheNamedPeriodThatCoversIt(t *testing.T) {
	cfg := testConfig(t).Series

	for _, tc := range []struct {
		name       string
		span       time.Duration
		wantHourly bool
	}{
		{"an afternoon", 4 * time.Hour, false},
		{"a fortnight", 14 * 24 * time.Hour, false},
		{"six months", 182 * 24 * time.Hour, true},
	} {
		window, hourly, bucket := api.PeriodForSpanForTesting(cfg, tc.span)
		if window < tc.span {
			t.Errorf("%s: chose a %s window for a %s span — it does not cover the range", tc.name, window, tc.span)
		}
		if hourly != tc.wantHourly {
			t.Errorf("%s: hourly = %v, want %v", tc.name, hourly, tc.wantHourly)
		}
		if bucket <= 0 {
			t.Errorf("%s: bucket = %s, want a positive resolution", tc.name, bucket)
		}
	}
}

// The error names what may actually be sent, custom included.
func TestUnknownPeriodErrorNamesCustom(t *testing.T) {
	d := deps(t, fixture(t))
	d.Store = &stubSource{slug: "sofia", points: []store.Point{}}

	rec := serve(t, d, get("/api/v1/sensor/42/series?metric=P2&period=forever", "203.0.113.64"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, api.CustomPeriod) {
		t.Errorf("the error does not offer %q: %s", api.CustomPeriod, body)
	}
}
