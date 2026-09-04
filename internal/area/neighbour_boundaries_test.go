package area_test

import (
	"testing"

	"airbg.org/internal/area"
	"airbg.org/internal/upstream"
)

// countryFiles is the committed boundary file for every code in airbg.yaml's
// upstream.countries. Kept beside the tests that use it rather than derived
// from the config, because the point of these tests is to catch the two lists
// drifting apart: a code enabled in config with no file here ingests nothing,
// and a file here with no code in config filters nothing.
var countryFiles = []struct{ path, code string }{
	{"../../data/boundaries/bulgaria.geojson", "BG"},
	{"../../data/boundaries/greece.geojson", "GR"},
	{"../../data/boundaries/north-macedonia.geojson", "MK"},
	{"../../data/boundaries/romania.geojson", "RO"},
	{"../../data/boundaries/serbia.geojson", "RS"},
	{"../../data/boundaries/turkey.geojson", "TR"},
}

// TestEveryEnabledCountryImportsWithItsCode is the precondition for the whole
// allow list. FilterByBoundary scopes its boundary set by country_code, so a
// boundary imported without one — or with the wrong one — is invisible to the
// filter and silently ingests nothing from that country, which looks exactly
// like a country that genuinely has no sensors.
func TestEveryEnabledCountryImportsWithItsCode(t *testing.T) {
	ctx, pool := migrated(t)

	for _, f := range countryFiles {
		n, err := area.Import(ctx, pool, f.path, area.NationalBoundaryKind)
		if err != nil {
			t.Fatalf("Import(%s): %v", f.path, err)
		}
		if n != 1 {
			t.Errorf("Import(%s) = %d features, want 1 — a national boundary is one shape", f.path, n)
		}
	}

	rows, err := pool.Query(ctx,
		`SELECT country_code, count(*) FROM area WHERE kind = $1
		 GROUP BY country_code ORDER BY country_code`,
		area.NationalBoundaryKind)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[code] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, f := range countryFiles {
		if got[f.code] != 1 {
			t.Errorf("country_code %q has %d rows, want 1", f.code, got[f.code])
		}
	}
	if len(got) != len(countryFiles) {
		t.Errorf("%d distinct country codes, want %d: %v", len(got), len(countryFiles), got)
	}
}

// TestCountryRowWithoutACodeIsRejectedByTheDatabase is the last line of defence
// under Import's own validation. It exists because the obvious spelling of this
// constraint does not work: `kind = 'country' AND country_code ~ '...'` OR
// `kind <> 'country' AND country_code IS NULL` evaluates to NULL for a country
// row with no code, and a CHECK only rejects FALSE — so the row went in. Any
// future rewrite of the constraint has to keep failing this insert.
func TestCountryRowWithoutACodeIsRejectedByTheDatabase(t *testing.T) {
	ctx, pool := migrated(t)

	for _, tc := range []struct {
		name string
		kind string
		code any
	}{
		{"country without a code", "country", nil},
		{"country with an alpha-3 code", "country", "BGR"},
		{"country with a lower-case code", "country", "bg"},
		{"non-country carrying a code", "oblast", "BG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx,
				`INSERT INTO area (slug, kind, name_bg, name_en, country_code, geom)
				 VALUES ($1, $2, 'Тест', 'Test', $3,
				         ST_Multi(ST_SetSRID(ST_MakeEnvelope(23, 42, 24, 43), 4326))::geography)`,
				"probe-"+tc.name, tc.kind, tc.code)
			if err == nil {
				t.Error("insert succeeded, want a check-constraint violation")
			}
		})
	}
}

// TestFilterAdmitsNeighboursAndStampsTheRightCountry is the behavioural test
// for the widened filter. One capital per enabled country, all in one batch:
// each must be accepted, and each must be stamped with its own code and not a
// neighbour's. Stamping matters as much as acceptance — the hex payload names
// a bin's country from these codes, so a sensor admitted under the wrong
// boundary is worse than one rejected.
func TestFilterAdmitsNeighboursAndStampsTheRightCountry(t *testing.T) {
	ctx, pool := migrated(t)
	for _, f := range countryFiles {
		if _, err := area.Import(ctx, pool, f.path, area.NationalBoundaryKind); err != nil {
			t.Fatalf("Import(%s): %v", f.path, err)
		}
	}

	// Capitals rather than border towns: a capital is unambiguously interior,
	// so a failure here is a filter bug and never a question about where a
	// disputed border actually runs.
	capitals := []struct {
		id       int64
		lon, lat float64
		code     string
	}{
		{1, 23.3327, 42.6957, "BG"}, // Sofia
		{2, 23.7275, 37.9838, "GR"}, // Athens
		{3, 21.4254, 41.9981, "MK"}, // Skopje
		{4, 26.1025, 44.4268, "RO"}, // Bucharest
		{5, 20.4489, 44.7866, "RS"}, // Belgrade
		{6, 28.9784, 41.0082, "TR"}, // Istanbul
	}
	var rs []upstream.Reading
	for _, c := range capitals {
		rs = append(rs, reading(c.id, c.lon, c.lat))
	}
	// Sensor 48524's London coordinates: still rejected by the widened list,
	// because widening added countries, not leniency.
	rs = append(rs, reading(48524, -0.1276, 51.5074))

	enabled := make([]string, 0, len(countryFiles))
	for _, f := range countryFiles {
		enabled = append(enabled, f.code)
	}

	res, err := area.FilterByBoundary(ctx, pool, rs, enabled)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !res.BoundaryPresent {
		t.Fatal("BoundaryPresent = false, want true")
	}
	if len(res.MissingCountries) != 0 {
		t.Errorf("MissingCountries = %v, want none — every enabled country has a committed boundary", res.MissingCountries)
	}
	if len(res.Accepted) != len(capitals) {
		t.Errorf("accepted %d readings, want %d", len(res.Accepted), len(capitals))
	}
	if res.RejectedSensors != 1 {
		t.Errorf("RejectedSensors = %d, want 1 (sensor 48524)", res.RejectedSensors)
	}
	for _, c := range capitals {
		if got := res.Country[c.id]; got != c.code {
			t.Errorf("sensor %d stamped %q, want %q", c.id, got, c.code)
		}
	}
	if _, ok := res.Country[48524]; ok {
		t.Error("sensor 48524 was stamped with a country; it is outside every enabled boundary")
	}
}

// TestDisabledCountryIsRejectedEvenThoughImported pins the reason the allow
// list is applied at query time rather than by what happens to be imported:
// disabling a country must be a config change, not a re-import, and a boundary
// left in the table must not keep admitting sensors.
func TestDisabledCountryIsRejectedEvenThoughImported(t *testing.T) {
	ctx, pool := migrated(t)
	for _, f := range countryFiles {
		if _, err := area.Import(ctx, pool, f.path, area.NationalBoundaryKind); err != nil {
			t.Fatalf("Import(%s): %v", f.path, err)
		}
	}

	// Athens, with Greece imported but not enabled.
	rs := []upstream.Reading{reading(7, 23.7275, 37.9838)}
	res, err := area.FilterByBoundary(ctx, pool, rs, []string{"BG"})
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if len(res.Accepted) != 0 {
		t.Errorf("accepted %d readings, want 0 — GR is imported but not in the allow list", len(res.Accepted))
	}
	if res.RejectedSensors != 1 {
		t.Errorf("RejectedSensors = %d, want 1", res.RejectedSensors)
	}
}

// TestMissingBoundaryIsReportedRatherThanSilent covers the intermediate state
// an operator hits when widening the allow list before sourcing the geometry.
// It is not an error — the other countries keep ingesting — but it must not be
// silent, because "no boundary" and "no sensors" produce the same empty result.
func TestMissingBoundaryIsReportedRatherThanSilent(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "../../data/boundaries/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	rs := []upstream.Reading{reading(8, 23.3327, 42.6957)} // Sofia
	res, err := area.FilterByBoundary(ctx, pool, rs, []string{"BG", "GR"})
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !res.BoundaryPresent {
		t.Fatal("BoundaryPresent = false, want true — BG is imported")
	}
	if len(res.Accepted) != 1 {
		t.Errorf("accepted %d readings, want 1 — a missing GR boundary must not stop BG ingesting", len(res.Accepted))
	}
	if len(res.MissingCountries) != 1 || res.MissingCountries[0] != "GR" {
		t.Errorf("MissingCountries = %v, want [GR]", res.MissingCountries)
	}
}
