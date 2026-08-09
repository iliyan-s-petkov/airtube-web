package area_test

import (
	"testing"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/upstream"
)

// reading builds a minimal upstream.Reading for these tests. There is no
// country parameter: upstream.Reading carries no Country field to filter on
// — trusting that field is exactly the bug this task fixes — so every test
// below decides acceptance purely from (lon, lat).
func reading(id int64, lon, lat float64) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "SDS011",
		Lon:        lon,
		Lat:        lat,
		Metric:     "P1",
		Value:      10,
		Timestamp:  time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC),
	}
}

func TestFilterByBoundaryAcceptsSensorInsideBulgaria(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Sofia.
	rs := []upstream.Reading{reading(1, 23.3327, 42.6957)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Fatal("boundaryPresent = false, want true")
	}
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0", rejected)
	}
	if len(accepted) != 1 {
		t.Fatalf("accepted = %d readings, want 1", len(accepted))
	}
}

// TestFilterByBoundaryRejectsSensor48524 is the regression test for the
// entire task. Sensor 48524 reports country "BG" (see reading's comment —
// upstream.Reading carries no country field precisely because it must not be
// trusted) but sits at London's coordinates. Against the pre-task-17
// behaviour — no geometric filter at all — this reading would sail straight
// through to scoring and storage; this test must fail against that
// behaviour and pass once ST_Covers against the real national boundary
// rejects it.
func TestFilterByBoundaryRejectsSensor48524(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// London, as reported by the real sensor 48524 in upstream data.
	rs := []upstream.Reading{reading(48524, -0.1276, 51.5074)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Fatal("boundaryPresent = false, want true")
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1 — sensor 48524's London coordinates must be rejected regardless of its self-reported country", rejected)
	}
	if len(accepted) != 0 {
		t.Fatalf("accepted = %d readings, want 0", len(accepted))
	}
}

// TestFilterByBoundaryRejectsPointInsideBoundingBoxButOutsideBulgaria proves
// the filter uses the real polygon, not a lon/lat bounding box.
//
// The fixture polygon's own bounding box is lon 22.50-28.00, lat
// 41.30-44.18 (its extreme vertices: (22.50,42.90) west, (28.00,43.75)
// east, (23.70,41.30) south, (27.20,44.18) north). A test point must land
// inside that box — so a bbox-based filter would wrongly accept it — while
// landing outside the polygon itself.
//
// (22.60, 41.60), just west of North Macedonia's border with Bulgaria near
// Kyustendil (the fixture's southwestern edge runs through (23.00, 41.60)),
// satisfies both: it is inside the box (22.50 <= 22.60, 41.30 <= 41.60) but
// west of — outside — the real boundary at that latitude. Round-tripping
// this same point through ST_Covers on a box built from the fixture's own
// bounding coordinates (instead of the real polygon) would accept it,
// which is exactly the gap this test pins.
func TestFilterByBoundaryRejectsPointInsideBoundingBoxButOutsideBulgaria(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Confirm the premise: a bounding box built from the fixture's own
	// extreme coordinates does admit this point. If this assertion itself
	// ever failed, the test below would no longer be proving anything about
	// ST_Covers versus a box.
	const boxMinLon, boxMaxLon = 22.50, 28.00
	const boxMinLat, boxMaxLat = 41.30, 44.18
	const testLon, testLat = 22.60, 41.60
	if testLon < boxMinLon || testLon > boxMaxLon || testLat < boxMinLat || testLat > boxMaxLat {
		t.Fatalf("test point (%v, %v) is outside the fixture's own bounding box (lon %v-%v, lat %v-%v) — this test would no longer demonstrate anything about ST_Covers versus a box",
			testLon, testLat, boxMinLon, boxMaxLon, boxMinLat, boxMaxLat)
	}

	rs := []upstream.Reading{reading(2, testLon, testLat)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Fatal("boundaryPresent = false, want true")
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1 — a bounding box built from the fixture's own extreme coordinates would wrongly accept this point; the real boundary must reject it", rejected)
	}
	if len(accepted) != 0 {
		t.Fatalf("accepted = %d readings, want 0", len(accepted))
	}
}

func TestFilterByBoundaryAcceptsPointJustInsideBoundary(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Plovdiv: well inside the interior, away from any edge ambiguity.
	rs := []upstream.Reading{reading(3, 24.7453, 42.1354)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Fatal("boundaryPresent = false, want true")
	}
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0", rejected)
	}
	if len(accepted) != 1 {
		t.Fatalf("accepted = %d readings, want 1", len(accepted))
	}
}

// TestFilterByBoundaryCoordinateOrderNotSwapped guards the same invariant
// internal/store and this package's own AssignSensors tests guard:
// geography is (longitude, latitude). Bulgaria spans lon 22-29, lat 41-45 —
// ranges that do not overlap — so swapping a genuinely Bulgarian sensor's
// coordinates must land it outside the boundary, and the filter must reject
// it. If FilterByBoundary (or the SQL it issues) ever swapped the arguments
// to ST_MakePoint, this in-range Sofia point would still incorrectly pass.
func TestFilterByBoundaryCoordinateOrderNotSwapped(t *testing.T) {
	ctx, pool := migrated(t)
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Sofia's real (lon, lat) is (23.3327, 42.6957). Swapped, that becomes
	// lon 42.6957, lat 23.3327 — nowhere near Bulgaria (lon 42.7 is in the
	// Middle East, lat 23.3 is well south of it).
	rs := []upstream.Reading{reading(4, 42.6957, 23.3327)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Fatal("boundaryPresent = false, want true")
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1 — a swapped coordinate must be rejected, not silently accepted", rejected)
	}
	if len(accepted) != 0 {
		t.Fatalf("accepted = %d readings, want 0 — coordinate order must not be silently swapped", len(accepted))
	}
}

func TestFilterByBoundaryAbsentBoundaryReportsNotPresent(t *testing.T) {
	ctx, pool := migrated(t)
	// Deliberately no area.Import call: fresh database, boundary never
	// loaded.

	rs := []upstream.Reading{reading(5, 23.3327, 42.6957)}
	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, rs)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if present {
		t.Fatal("boundaryPresent = true, want false — no boundary of kind country was ever imported")
	}
	if accepted != nil {
		t.Errorf("accepted = %v, want nil when boundary is absent", accepted)
	}
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0 when boundary is absent (caller decides, not this function)", rejected)
	}
}

func TestFilterByBoundaryEmptyBatchReportsPresentWithoutQuerying(t *testing.T) {
	ctx, pool := migrated(t)
	// No boundary imported either — this must still report present=true for
	// an empty batch, since there is nothing to test and no reason to
	// surface a false "boundary absent" signal on a cycle where upstream
	// simply returned nothing.

	accepted, rejected, present, err := area.FilterByBoundary(ctx, pool, nil)
	if err != nil {
		t.Fatalf("FilterByBoundary: %v", err)
	}
	if !present {
		t.Error("boundaryPresent = false, want true for an empty batch")
	}
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0", rejected)
	}
	if len(accepted) != 0 {
		t.Errorf("accepted = %d, want 0", len(accepted))
	}
}
