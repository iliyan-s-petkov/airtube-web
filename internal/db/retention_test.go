package db_test

import "testing"

func TestReadingHourlyIsHypertable(t *testing.T) {
	ctx, pool := migrated(t)

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'reading_hourly'`).Scan(&count)
	if err != nil {
		t.Fatalf("query hypertables: %v", err)
	}
	if count != 1 {
		t.Fatalf("reading_hourly is not a hypertable (found %d)", count)
	}
}

// TestRetentionPoliciesExist asserts the drop_after interval, not merely that a
// policy exists. The interval is the whole content of the requirement: raw
// readings are kept 30 days and hourly buckets 2 years, and a policy with the
// wrong interval deletes real data on a schedule while every existence check
// still passes. Getting these two confused is also plausible in a way a missing
// policy is not — they are adjacent lines in the same migration.
func TestRetentionPoliciesExist(t *testing.T) {
	ctx, pool := migrated(t)

	rows, err := pool.Query(ctx,
		`SELECT hypertable_name, config ->> 'drop_after'
		 FROM timescaledb_information.jobs
		 WHERE proc_name = 'policy_retention'`)
	if err != nil {
		t.Fatalf("query jobs: %v", err)
	}
	defer rows.Close()

	found := map[string]string{}
	for rows.Next() {
		var name, dropAfter string
		if err := rows.Scan(&name, &dropAfter); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[name] = dropAfter
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// Postgres renders an interval of 2 years as "2 years" and one of 30 days as
	// "30 days"; both are the canonical output for the literals the migration
	// passes to add_retention_policy.
	for _, want := range []struct{ table, dropAfter string }{
		{"reading", "30 days"},
		{"reading_hourly", "2 years"},
	} {
		got, ok := found[want.table]
		if !ok {
			t.Errorf("no retention policy on %s", want.table)
			continue
		}
		if got != want.dropAfter {
			t.Errorf("%s retention drop_after = %q, want %q", want.table, got, want.dropAfter)
		}
	}
}
