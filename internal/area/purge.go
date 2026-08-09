package area

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/db"
)

// PurgeResult reports what PurgeOutsideBoundary removed. Orphaned readings are
// counted separately from sensors because they have a separate cause (see
// PurgeOutsideBoundary's doc comment) and an operator needs to be able to tell
// "we ingested foreign sensors" from "someone backfilled a sensor_id that was
// never ingested".
type PurgeResult struct {
	SensorsRemoved    int64
	OrphanRawRows     int64
	OrphanHourlyRows  int64
	ReadingsRemoved   int64
	HourlyRowsRemoved int64
}

// PurgeOutsideBoundary deletes every sensor (and its stored raw and hourly
// readings) whose location fails ST_Covers against the
// area.kind = NationalBoundaryKind boundary, and reports how many sensors
// were removed.
//
// This is task-17 review finding 4's cleanup: FilterByBoundary only guards
// data ingested after this task shipped. Sensors trusted in under the old
// country-field check (sensor 48524 among them) already have rows in
// `sensor`, `reading` and `reading_hourly`, and — quality.Score being
// purely in-batch — nothing about going forward reaches back to fix that;
// 48524 would otherwise keep rendering in London indefinitely.
//
// This function is deliberately not called from anywhere else in this
// codebase — not from Import, not from FilterByBoundary, not from ingest's
// RunOnce, not on startup. Deleting stored data must always be a single,
// deliberate, operator-invoked action (the `airbg purge-outside-boundary`
// CLI subcommand), never an automatic side effect of importing a boundary
// or of an ordinary ingest cycle. An accidental or partial boundary import
// must not be able to silently wipe sensors.
//
// It fails closed, matching FilterByBoundary: if no NationalBoundaryKind
// boundary exists, it refuses to run and returns an error, rather than
// treating "no boundary to test against" as "nothing qualifies for
// deletion" — the latter would report success (0 removed) while having
// silently done nothing, which looks identical to "the database is
// already clean" and would mask the same missing-import condition
// FilterByBoundary's own fail-closed policy exists to surface.
//
// # Orphaned readings
//
// Discovering rows to delete by selecting from `sensor` is not sufficient on
// its own, and this was a reachability hole rather than a theoretical one.
// `reading.sensor_id` has no foreign key to `sensor(sensor_id)` (reading is a
// hypertable), and `airbg backfill <sensor_id> <csv>` writes straight to
// reading_hourly with an operator-supplied sensor_id and no boundary check. So
// rows could exist for a sensor_id that has no `sensor` row at all — invisible
// to a sensor-driven purge. The one command documented as the cleanup for
// foreign data provably could not reach backfilled foreign data.
//
// Both halves of that are now closed: backfill refuses an unknown or
// out-of-boundary sensor_id up front (see backfill.CheckSensorInBoundary), and
// this function additionally deletes reading/reading_hourly rows with no
// corresponding `sensor` row, so any orphan already stored — including one
// written before that check existed — is reachable. Orphans are reported
// separately from sensors, because an orphan is not evidence of a foreign
// sensor; it is evidence that something wrote readings for a sensor that was
// never ingested, which is a different thing for an operator to know.
func PurgeOutsideBoundary(ctx context.Context, pool *pgxpool.Pool) (PurgeResult, error) {
	var result PurgeResult

	var present bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM area WHERE kind = $1)`, NationalBoundaryKind).
		Scan(&present); err != nil {
		return result, err
	}
	if !present {
		return result, fmt.Errorf(
			"area: no boundary of kind %q imported — refusing to purge (run: airbg import-areas <path.geojson> %s)",
			NationalBoundaryKind, NationalBoundaryKind)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	// The deletes below are bulk deletes over a hypertable spanning 30 daily
	// chunks; at production volume (~900 sensors x 7 metrics x 30 days of
	// 5-minute samples) they plausibly exceed the pool-wide 15s
	// statement_timeout. Because they run inside this transaction, a timeout
	// would abort the entire purge, so the documented cleanup could never
	// complete however many times the operator retried. Raised for this
	// transaction only — the pool default is unchanged and still protects the
	// collector's and Phase 2's query paths.
	if err := db.SetLocalStatementTimeout(ctx, tx, db.OperatorStatementTimeout); err != nil {
		return result, err
	}

	rows, err := tx.Query(ctx, `
		SELECT s.sensor_id
		FROM sensor s
		WHERE NOT EXISTS (
			SELECT 1 FROM area a
			WHERE a.kind = $1 AND ST_Covers(a.geom, s.location)
		)`, NationalBoundaryKind)
	if err != nil {
		return result, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	if len(ids) > 0 {
		// reading and reading_hourly carry no foreign key to sensor (reading is
		// a hypertable; see internal/db/migrations/00002_core_schema.sql), so
		// deleting the sensor row alone would leave orphaned history behind.
		// Clean up explicitly, in dependency order, inside the same transaction
		// as the sensor deletion so this is all-or-nothing.
		hourly, err := tx.Exec(ctx, `DELETE FROM reading_hourly WHERE sensor_id = ANY($1)`, ids)
		if err != nil {
			return result, err
		}
		result.HourlyRowsRemoved = hourly.RowsAffected()

		raw, err := tx.Exec(ctx, `DELETE FROM reading WHERE sensor_id = ANY($1)`, ids)
		if err != nil {
			return result, err
		}
		result.ReadingsRemoved = raw.RowsAffected()

		// area_sensor references sensor with ON DELETE CASCADE
		// (00004_areas.sql), so it needs no explicit statement here.
		tag, err := tx.Exec(ctx, `DELETE FROM sensor WHERE sensor_id = ANY($1)`, ids)
		if err != nil {
			return result, err
		}
		result.SensorsRemoved = tag.RowsAffected()
	}

	// Orphans: readings whose sensor_id has no `sensor` row at all. Run after
	// the sensor deletion above so its own cleanup is already accounted for
	// and cannot be double-counted here.
	orphanHourly, err := tx.Exec(ctx, `
		DELETE FROM reading_hourly r
		WHERE NOT EXISTS (SELECT 1 FROM sensor s WHERE s.sensor_id = r.sensor_id)`)
	if err != nil {
		return result, err
	}
	result.OrphanHourlyRows = orphanHourly.RowsAffected()

	orphanRaw, err := tx.Exec(ctx, `
		DELETE FROM reading r
		WHERE NOT EXISTS (SELECT 1 FROM sensor s WHERE s.sensor_id = r.sensor_id)`)
	if err != nil {
		return result, err
	}
	result.OrphanRawRows = orphanRaw.RowsAffected()

	if err := tx.Commit(ctx); err != nil {
		return PurgeResult{}, err
	}
	return result, nil
}
