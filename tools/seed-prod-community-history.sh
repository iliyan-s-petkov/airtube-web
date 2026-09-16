#!/usr/bin/env bash
# Copies sensor.community history from staging into production. Additive and
# re-runnable. See tools/README-seed-prod-community-history.md.
set -uo pipefail

STAGING_SSH=${STAGING_SSH:-"ssh -i $HOME/.ssh/id_ed25519_home_infra ubuntu@192.168.1.176"}
PROD_SSH=${PROD_SSH:-"ssh airbg-prod"}
FROM_DAY=${FROM_DAY:-2026-08-31}
TO_DAY=${TO_DAY:-2026-09-17}
SOURCE=sensor.community

# psql runs inside the database container on each host so the credentials stay
# in the container's environment and never reach this script or a log.
install_wrappers() {
  cat <<'EOS' | $PROD_SSH 'cat > /tmp/prdsql.sh && chmod +x /tmp/prdsql.sh'
#!/bin/sh
cd /srv/airbg && exec sudo -n docker compose -f docker-compose.prod.yml exec -T db \
  sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' _ "$@"
EOS
  cat <<'EOS' | $STAGING_SSH 'cat > /tmp/stgsql.sh && chmod +x /tmp/stgsql.sh'
#!/bin/sh
exec sudo -n docker exec -i airbg-db-1 \
  sh -c 'psql -v ON_ERROR_STOP=1 -q -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"' _ "$@"
EOS
}

prd() { $PROD_SSH '/tmp/prdsql.sh'; }
stg_copy() { $STAGING_SSH "/tmp/stgsql.sh -c \"COPY ($1) TO STDOUT\""; }
say() { printf '\n=== %s ===\n' "$*" >&2; }

# GNU first, BSD second: this runs from a Linux sandbox more often than a Mac,
# and `date -j` on GNU fails with an unhelpful "invalid option".
next_day() { date -d "$1 +1 day" +%Y-%m-%d 2>/dev/null || date -j -f %Y-%m-%d -v+1d "$1" +%Y-%m-%d; }

install_wrappers

say "staging tables"
prd <<'SQL'
CREATE UNLOGGED TABLE IF NOT EXISTS seed_sensor  (LIKE sensor);
CREATE UNLOGGED TABLE IF NOT EXISTS seed_hourly  (LIKE reading_hourly);
CREATE UNLOGGED TABLE IF NOT EXISTS seed_reading (LIKE reading);
SQL

say "sensors"
stg_copy "SELECT * FROM sensor WHERE source = '$SOURCE'" |
  $PROD_SSH '/tmp/prdsql.sh -c "TRUNCATE seed_sensor; COPY seed_sensor FROM STDIN"'
prd <<'SQL'
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM seed_sensor WHERE source <> 'sensor.community') THEN
    RAISE EXCEPTION 'refusing to seed: an official station reached the staging table';
  END IF;
END $$;
INSERT INTO sensor SELECT * FROM seed_sensor ON CONFLICT (sensor_id) DO NOTHING;
TRUNCATE seed_sensor;
SQL

# The rollup is what every historical read goes through — replay frames, the
# 24h/48h/7d map windows, area series (internal/store/frames.go, window.go,
# aggregate.go). Seeding this alone is what makes history appear.
say "hourly rollups"
stg_copy "SELECT h.* FROM reading_hourly h JOIN sensor s USING (sensor_id) WHERE s.source = '$SOURCE'" |
  $PROD_SSH '/tmp/prdsql.sh -c "TRUNCATE seed_hourly; COPY seed_hourly FROM STDIN"'
prd <<'SQL'
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM seed_hourly x JOIN sensor s USING (sensor_id)
             WHERE s.source <> 'sensor.community') THEN
    RAISE EXCEPTION 'refusing to seed: an official station reached the staging table';
  END IF;
END $$;
INSERT INTO reading_hourly SELECT * FROM seed_hourly ON CONFLICT DO NOTHING;
TRUNCATE seed_hourly;
SQL

# Raw readings a day at a time: 36M rows in one statement is one transaction,
# one failure mode and no way to resume. Retention drops raw at 30 days, so the
# oldest of these expire within a fortnight; the rollup above is the durable part.
day=$FROM_DAY
while [[ "$day" < "$TO_DAY" ]]; do
  next=$(next_day "$day")
  say "raw readings $day"
  stg_copy "SELECT r.* FROM reading r JOIN sensor s USING (sensor_id)
             WHERE s.source = '$SOURCE' AND r.time >= '$day' AND r.time < '$next'" |
    $PROD_SSH '/tmp/prdsql.sh -c "TRUNCATE seed_reading; COPY seed_reading FROM STDIN"'
  $PROD_SSH '/tmp/prdsql.sh -c "INSERT INTO reading SELECT * FROM seed_reading ON CONFLICT DO NOTHING; TRUNCATE seed_reading"'
  day=$next
done

say "cleanup"
prd <<'SQL'
DROP TABLE IF EXISTS seed_sensor, seed_hourly, seed_reading;
SQL
$PROD_SSH 'rm -f /tmp/prdsql.sh'
$STAGING_SSH 'rm -f /tmp/stgsql.sh'

say "result"
prd <<'SQL'
\pset pager off
SELECT s.source, count(*) AS raw_rows, min(r.time)::date AS from_d, max(r.time)::date AS to_d
  FROM reading r JOIN sensor s USING (sensor_id) GROUP BY 1 ORDER BY 1;
SELECT s.source, count(*) AS hourly_rows, min(h.bucket)::date AS from_d, max(h.bucket)::date AS to_d
  FROM reading_hourly h JOIN sensor s USING (sensor_id) GROUP BY 1 ORDER BY 1;
SQL
