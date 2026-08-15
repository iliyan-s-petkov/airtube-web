package area

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/db"
)

// AssignSensors recomputes the sensor-to-area mapping by point-in-polygon
// containment, in both directions: memberships that now hold are inserted, and
// memberships that no longer hold are withdrawn.
//
// The withdrawal half is not optional, and its absence was a real defect. This
// function used to be `INSERT ... ON CONFLICT DO NOTHING` alone, with no DELETE
// anywhere in the codebase (purge.go relies only on ON DELETE CASCADE from a
// sensor deletion) — while its doc comment already claimed to "recompute" the
// mapping. Two ordinary events made that claim false:
//
//   - A sensor relocates. store.go states outright that "Location is refreshed
//     on conflict because sensors are occasionally relocated upstream", and does
//     exactly that. A sensor moving from Sofia to Plovdiv got its new location
//     written and a Plovdiv membership added, and kept its Sofia membership
//     forever.
//   - A boundary is re-imported smaller. Import replaces geom on conflict, but
//     memberships derived from the old, larger polygon were never withdrawn.
//
// Phase 2's "which sensors are in area X" query reads area_sensor directly, so
// in both cases it would over-report, permanently and silently. "Sensors do not
// move" (the previous comment's premise) is contradicted by the store's own
// stated behaviour; the mapping is cheap to recompute and must be authoritative
// rather than cumulative.
//
// Delete and insert run in one transaction, so no reader can observe a moment
// where memberships have been withdrawn but not yet re-established.
//
// A sensor may belong to several areas at once: a Sofia sensor is in the Sofia
// city polygon, the Sofia-grad oblast, and its neighbourhood. That is intended,
// and the composite primary key keeps each pairing unique.
//
// area.kind = NationalBoundaryKind ("country") is excluded (task-17 review
// finding 2). That boundary exists solely for FilterByBoundary's ingest-time
// membership test; every sensor that reaches this query has, by
// construction, already passed it. Assigning it to a whole-country
// pseudo-area here would add a meaningless row to every "which areas is
// this sensor in" / "which sensors are in this area" query — exactly the
// confusion NationalBoundaryKind's own doc comment says a distinct kind
// exists to prevent. The delete below also removes any such row an earlier
// (pre-task-17) version of this code may already have written.
func AssignSensors(ctx context.Context, pool *pgxpool.Pool, assignTimeout time.Duration) (assigned, revoked int64, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	// The full area x sensor ST_Covers join below is the most expensive query
	// the collector runs, and it runs on every poll cycle. The pool-wide default
	// statement_timeout is the right bound for an ordinary read or write but
	// too tight for this one at neighbourhood-level area granularity, where
	// exceeding it would fail every cycle rather than degrade. Raised for this
	// transaction only, so the protection stays in place everywhere else.
	if err := db.SetLocalStatementTimeout(ctx, tx, db.StatementTimeoutValue(assignTimeout)); err != nil {
		return 0, 0, err
	}

	// Withdraw memberships that containment no longer supports. Covers both a
	// sensor that moved out of the polygon and a polygon that shrank away from
	// the sensor, plus any stale country-kind row. Rows whose area or sensor
	// row is gone entirely need no clause here: area_sensor's foreign keys
	// cascade those away already (00004_areas.sql).
	del, err := tx.Exec(ctx,
		`DELETE FROM area_sensor m
		 USING area a
		 WHERE m.area_slug = a.slug
		   AND (
		     a.kind = $1
		     OR NOT EXISTS (
		       SELECT 1 FROM sensor s
		       WHERE s.sensor_id = m.sensor_id
		         AND ST_Covers(a.geom, s.location)
		     )
		   )`,
		NationalBoundaryKind)
	if err != nil {
		return 0, 0, err
	}

	ins, err := tx.Exec(ctx,
		`INSERT INTO area_sensor (area_slug, sensor_id)
		 SELECT a.slug, s.sensor_id
		 FROM area a
		 JOIN sensor s ON ST_Covers(a.geom, s.location)
		 WHERE a.kind != $1
		 ON CONFLICT (area_slug, sensor_id) DO NOTHING`,
		NationalBoundaryKind)
	if err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return ins.RowsAffected(), del.RowsAffected(), nil
}
