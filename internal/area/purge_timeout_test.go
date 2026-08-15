package area_test

import (
	"strings"
	"testing"
	"time"

	"airbg.org/internal/area"
)

// TestPurgeOutsideBoundaryAppliesTheGivenTimeout proves operatorTimeout
// genuinely reaches PurgeOutsideBoundary's transaction, rather than being
// accepted and ignored. A mutation that hardcoded its
// SetLocalStatementTimeout call to a fixed value passed every other test in
// this package silently, because none of them seed enough rows for the
// outside-boundary scan or the bulk deletes to take measurable time — this
// test exists to close exactly that gap.
//
// 200,000 sensors scattered across the globe (almost all outside Bulgaria,
// the imported boundary) make the NOT EXISTS / ST_Covers scan and the
// subsequent bulk deletes take low-single-digit milliseconds; a 1ms budget
// is far below that, so a real Postgres statement_timeout cancellation is
// the only way this can fail with the error asserted below.
func TestPurgeOutsideBoundaryAppliesTheGivenTimeout(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 SELECT g, 'SDS011',
		   ST_SetSRID(ST_MakePoint(-180.0 + random()*360.0, -80.0 + random()*160.0), 4326)::geography
		 FROM generate_series(1, 200000) g`); err != nil {
		t.Fatalf("seed sensors: %v", err)
	}

	if _, err := area.PurgeOutsideBoundary(ctx, pool, 1*time.Millisecond); err == nil {
		t.Fatal("PurgeOutsideBoundary with a 1ms timeout succeeded against 200,000 scattered sensors — the configured timeout is not reaching the session")
	} else if !strings.Contains(err.Error(), "canceling statement due to statement timeout") {
		t.Errorf("err = %v, want a statement timeout cancellation", err)
	}
}
