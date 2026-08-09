package area

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
func PurgeOutsideBoundary(ctx context.Context, pool *pgxpool.Pool) (removed int64, err error) {
	var present bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM area WHERE kind = $1)`, NationalBoundaryKind).
		Scan(&present); err != nil {
		return 0, err
	}
	if !present {
		return 0, fmt.Errorf(
			"area: no boundary of kind %q imported — refusing to purge (run: airbg import-areas <path.geojson> %s)",
			NationalBoundaryKind, NationalBoundaryKind)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	rows, err := tx.Query(ctx, `
		SELECT s.sensor_id
		FROM sensor s
		WHERE NOT EXISTS (
			SELECT 1 FROM area a
			WHERE a.kind = $1 AND ST_Covers(a.geom, s.location)
		)`, NationalBoundaryKind)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit(ctx)
	}

	// reading and reading_hourly carry no foreign key to sensor (reading is
	// a hypertable; see internal/db/migrations/00002_core_schema.sql), so
	// deleting the sensor row alone would leave orphaned history behind.
	// Clean up explicitly, in dependency order, inside the same transaction
	// as the sensor deletion so this is all-or-nothing.
	if _, err := tx.Exec(ctx, `DELETE FROM reading_hourly WHERE sensor_id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM reading WHERE sensor_id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	// area_sensor references sensor with ON DELETE CASCADE
	// (00004_areas.sql), so it needs no explicit statement here.
	tag, err := tx.Exec(ctx, `DELETE FROM sensor WHERE sensor_id = ANY($1)`, ids)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
