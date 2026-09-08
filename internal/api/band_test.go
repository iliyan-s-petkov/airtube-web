package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/store"
)

func sampleBands() []store.AreaBand {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	return []store.AreaBand{
		{Time: base, Low: 4, Median: 12.5, High: 40},
		{Time: base.Add(time.Hour), Low: 5, Median: 14, High: 44},
	}
}

func withBands(t *testing.T) (api.Deps, *stubSource) {
	t.Helper()
	src := &stubSource{slug: "sofia", points: samplePoints(), bands: sampleBands()}
	d := deps(t, snapshotWithAreaSeries(t, "sofia"))
	d.Store = src
	return d, src
}

func decodeBand(t *testing.T, raw []byte) struct {
	Times []time.Time `json:"t"`
	V     []float64   `json:"v"`
	Lo    []float64   `json:"lo"`
	Hi    []float64   `json:"hi"`
} {
	t.Helper()
	var body struct {
		Times []time.Time `json:"t"`
		V     []float64   `json:"v"`
		Lo    []float64   `json:"lo"`
		Hi    []float64   `json:"hi"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding the series body: %v", err)
	}
	return body
}

// The default metric and period are the combination the snapshot precomputes,
// and that precomputed body carries the median alone. A banded request for it
// must reach the database anyway, or the reader asks for the spread and is
// handed a body with no spread in it.
func TestBandSkipsTheSnapshotFastPath(t *testing.T) {
	d, src := withBands(t)

	rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h&band=1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if src.bandCalls != 1 {
		t.Errorf("AreaSeriesBand calls = %d, want 1 (the fast path served a banded request)", src.bandCalls)
	}

	body := decodeBand(t, rec.Body.Bytes())
	if len(body.Lo) != 2 || len(body.Hi) != 2 {
		t.Fatalf("lo=%v hi=%v, want two of each", body.Lo, body.Hi)
	}
	if body.Lo[0] != 4 || body.V[0] != 12.5 || body.Hi[0] != 40 {
		t.Errorf("first bucket = lo %v, v %v, hi %v; want 4, 12.5, 40", body.Lo[0], body.V[0], body.Hi[0])
	}
}

// The three columns are read positionally against t: a shorter one silently
// pairs a low with the wrong bucket's median.
func TestBandColumnsAreTheSameLengthAsTheTimes(t *testing.T) {
	d, _ := withBands(t)

	rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P1&period=24h&band=1"))
	body := decodeBand(t, rec.Body.Bytes())
	n := len(body.Times)
	if len(body.V) != n || len(body.Lo) != n || len(body.Hi) != n {
		t.Errorf("t=%d v=%d lo=%d hi=%d, want all equal", n, len(body.V), len(body.Lo), len(body.Hi))
	}
}

// A reader who did not ask for a band must not be charged for the two extra
// aggregates, and must get the bytes the plain series has always been.
func TestUnbandedRequestNeitherQueriesNorReturnsABand(t *testing.T) {
	d, src := withBands(t)

	rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P1&period=24h"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if src.bandCalls != 0 {
		t.Errorf("AreaSeriesBand calls = %d, want 0", src.bandCalls)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, ok := raw["lo"]; ok {
		t.Error(`an unbanded response carries "lo"`)
	}
	if _, ok := raw["hi"]; ok {
		t.Error(`an unbanded response carries "hi"`)
	}
}

// band=true or band=yes is a mistake worth naming. Read as "off" it would give
// a 200 and a plain series, and the caller would have no way to see why the
// spread never appeared.
func TestBandRejectsAnythingButZeroAndOne(t *testing.T) {
	d, src := withBands(t)

	for _, v := range []string{"true", "yes", "2", "-1"} {
		rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P1&period=24h&band="+v))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("band=%q: status = %d, want 400", v, rec.Code)
		}
	}
	if src.bandCalls != 0 || src.areaSeriesCalls != 0 {
		t.Errorf("a rejected band value reached the database: band=%d series=%d", src.bandCalls, src.areaSeriesCalls)
	}
}

// band=0 is the explicit spelling of the default, not a fourth error case: a
// client that always sends the parameter sends it off as well as on.
func TestBandZeroIsThePlainSeries(t *testing.T) {
	d, src := withBands(t)

	rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P1&period=24h&band=0"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if src.bandCalls != 0 {
		t.Errorf("AreaSeriesBand calls = %d, want 0", src.bandCalls)
	}
}

// The band is a per-area response like any other, so it must pass the same
// gates. Validation before the breadth check is what stops a rejected value
// from costing a slug in the caller's enumeration budget.
func TestBandIsRejectedForAnUnknownSlug(t *testing.T) {
	d, src := withBands(t)

	rec := serve(t, d, newSeriesRequest("/api/v1/area/atlantis/series?metric=P1&period=24h&band=1"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if src.bandCalls != 0 {
		t.Errorf("an unknown slug reached AreaSeriesBand")
	}
}

// The band answers a question about an area, and a database error answering it
// carries the SQL and the table names like any other pgx error.
func TestBandDatabaseErrorIsNotLeaked(t *testing.T) {
	d, src := withBands(t)
	src.err = errBoom

	rec := serve(t, d, newSeriesRequest("/api/v1/area/sofia/series?metric=P1&period=24h&band=1"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "boom") || strings.Contains(body, "reading") {
		t.Errorf("the database error leaked into the response: %s", body)
	}
}
