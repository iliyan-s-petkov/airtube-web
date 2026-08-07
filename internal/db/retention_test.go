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

func TestRetentionPoliciesExist(t *testing.T) {
	ctx, pool := migrated(t)

	rows, err := pool.Query(ctx,
		`SELECT hypertable_name FROM timescaledb_information.jobs
		 WHERE proc_name = 'policy_retention'`)
	if err != nil {
		t.Fatalf("query jobs: %v", err)
	}
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[name] = true
	}
	for _, want := range []string{"reading", "reading_hourly"} {
		if !found[want] {
			t.Errorf("no retention policy on %s", want)
		}
	}
}
