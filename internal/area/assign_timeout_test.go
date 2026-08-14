package area_test

import (
	"strings"
	"testing"
	"time"

	"airbg.org/internal/area"
)

// TestAssignSensorsAppliesTheGivenTimeout proves assignTimeout genuinely
// reaches the ST_Covers join's session, rather than being accepted and
// ignored. A mutation that hardcoded AssignSensors' SetLocalStatementTimeout
// call to a fixed value passed every other test in this package silently,
// because none of them seed enough rows for the join to take measurable
// time — this test exists to close exactly that gap.
//
// 200,000 scattered sensors make the join itself take low-single-digit
// milliseconds; a 1ms budget is far below that, so a real Postgres
// statement_timeout cancellation is the only way this can fail with the
// "canceling statement due to statement timeout" error asserted below.
func TestAssignSensorsAppliesTheGivenTimeout(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 SELECT g, 'SDS011',
		   ST_SetSRID(ST_MakePoint(23.0 + random()*1.0, 42.0 + random()*1.0), 4326)::geography
		 FROM generate_series(1, 200000) g`); err != nil {
		t.Fatalf("seed sensors: %v", err)
	}

	if _, _, err := area.AssignSensors(ctx, pool, 1*time.Millisecond); err == nil {
		t.Fatal("AssignSensors with a 1ms timeout succeeded against a 200,000-row join — the configured timeout is not reaching the session")
	} else if !strings.Contains(err.Error(), "canceling statement due to statement timeout") {
		t.Errorf("err = %v, want a statement timeout cancellation", err)
	}
}
