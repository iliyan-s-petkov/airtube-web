# Deploying airbg.org

This is the operator runbook for the production deployment: one VPS, one
Docker Compose stack, no orchestrator. It documents `deploy/docker-compose.prod.yml`,
`deploy/Caddyfile`, `deploy/nftables.conf`, `deploy/ofelia.ini` and
`deploy/.env.example` as they exist in this repository — if a command here
disagrees with one of those files, the file is correct and this document is
a bug.

## 1. What runs where

Five services, three Docker networks, one rule that makes the rest of this
document safe to follow: **only `caddy` publishes ports.**

| Service | Role | Networks |
|---|---|---|
| `caddy` | The only service the internet can open a socket to. Terminates TLS for both vhosts and reverse-proxies to `app`. Publishes `80` and `443`. | `edge` |
| `app` | The Go application: serves `airbg.org` on `:8080`, `tiles.airbg.org` on `:8082`, and Prometheus metrics on `:9090`. Publishes **no port**, in this file or any other, in any environment. | `edge`, `back` |
| `db` | TimescaleDB (Postgres). Publishes no port; reachable only from `app`, the one-shot backup jobs, and the one-shot `collect` job. | `back` (`internal: true`), `collect` |
| `socket-proxy` | `tecnativa/docker-socket-proxy`, holding the real Docker socket read-only and exposing only container creation (`CONTAINERS=1`, `POST=1`; every other endpoint group explicit `0`). | `sched` (`internal: true`) |
| `ofelia` | Scheduler. Runs `airbg collect` every 5 minutes and `pg_dump` nightly as one-shot containers, talking only to `socket-proxy`, never to the Docker daemon directly. | `sched` (`internal: true`) |

`back` and `sched` are marked `internal: true` in the compose file, so neither
has a route to the internet or to each other's neighbours: `ofelia` and
`socket-proxy` cannot reach `edge` or `back` except where `db` sits on `back`
for the app and the backup jobs to reach it, and `app` is not on `sched`.

`collect` is deliberately **not** `internal: true` — it exists only so the
`collect` job (run by `ofelia`, on the schedule in `deploy/ofelia.ini`) can
reach both `db` and `https://data.sensor.community` (`airbg.yaml`'s
`upstream.url`) from the same one-shot container. `back` can't do this job:
it's internal by design, so a container on it alone has no route out.
Widening `back` itself to grant that route was rejected — it's shared with
`app`, and `app` should keep the same no-egress posture `db` has. `ofelia`'s
INI format holds exactly one `network =` value per job (a second line
silently replaces the first instead of adding to it — see the comment above
the `collect` job in `deploy/ofelia.ini`), so a job can't simply list both
`back` and `collect`; `db` joining `collect` in addition to `back` is what
lets one job container reach both from one network. `caddy`, `app` and
`ofelia`/`socket-proxy` never join `collect` — only `db` and the transient
`collect` job containers do.

Because `app` publishes nothing, there is no URL and no port on the host that
reaches it directly. The only way to talk to the running container from a
workstation is an SSH local forward to its container address (§5) — never a
`ports:` line, in production or in any per-operator variant of this file.

## 2. First-run bootstrap

Run these in order. Each step depends on the one before it.

1. Provision the host, install Docker (Engine + Compose plugin), then:

   ```bash
   mkdir -p /srv/airbg /srv/airbg/tls /var/lib/airbg/tiles /var/backups/airbg
   ```

   `/var/backups/airbg` must exist before the first nightly backup job runs —
   `ofelia`'s backup job bind-mounts it and does not create it.

2. Install the firewall floor. Check the syntax before loading it into the
   kernel — this needs `NET_ADMIN`, or `nft` fails with "Operation not
   permitted", a capability problem, not a syntax error:

   ```bash
   docker run --rm --cap-add=NET_ADMIN -v "$PWD/deploy/nftables.conf:/etc/nftables.conf:ro" \
     alpine sh -c "apk add --no-cache nftables >/dev/null && nft -c -f /etc/nftables.conf"
   ```

   Then install it:

   ```bash
   cp deploy/nftables.conf /etc/nftables.conf
   systemctl enable --now nftables
   ```

   **Before you apply this**, confirm SSH is reachable on port 22 from where
   you're sitting — the ruleset's default policy on `input` is `drop`, and a
   mistake here locks you out with no console. `deploy/nftables.conf` is a
   floor, not the enforcement: it only stops something binding a port by
   accident, since Docker's published ports bypass the filter table's `input`
   chain entirely and `tiles.airbg.org` must stay reachable from the whole
   internet on 443 regardless.

3. In Cloudflare DNS: `airbg.org` proxied (orange cloud), `tiles.airbg.org`
   DNS-only (grey cloud), both pointing at the host's public IP.

4. Issue a Cloudflare Origin CA certificate for `airbg.org` (Cloudflare
   dashboard → SSL/TLS → Origin Server). Save the two halves as
   `/srv/airbg/tls/origin.pem` and `/srv/airbg/tls/origin.key` — those are the
   exact filenames `deploy/Caddyfile` references. Download Cloudflare's
   origin-pull CA certificate and save it as
   `/srv/airbg/tls/cloudflare-origin-pull-ca.pem`. Then:

   ```bash
   chown -R root:root /srv/airbg/tls
   chmod 600 /srv/airbg/tls/origin.pem /srv/airbg/tls/origin.key /srv/airbg/tls/cloudflare-origin-pull-ca.pem
   ```

   Finally, enable **Authenticated Origin Pulls** for the zone in the
   Cloudflare dashboard (SSL/TLS → Origin Server). This certificate is the one
   long-lived secret this design adds: it is valid for 15 years and lives on
   the box. Losing control of the host means rotating it.

   This is the actual enforcement, not the firewall: `deploy/Caddyfile`
   requires this client certificate (`client_auth`, `require_and_verify`) on
   the `airbg.org` vhost only. A packet filter can't do this job because
   `tiles.airbg.org` shares port 443 and must stay public — SNI is above the
   layer a filter operates at.

   If you run `caddy validate --config deploy/Caddyfile` with only the
   Caddyfile mounted (no `/srv/airbg/tls`), it will fail, complaining that
   `origin.pem`, `origin.key` or `cloudflare-origin-pull-ca.pem` cannot be
   found. That is expected — those files only exist on the host once this
   step is done — and is not a sign the Caddyfile itself is wrong. Validate
   it for real by pointing it at a checkout with the certs already in place,
   or defer validation to `docker compose up -d` in step 13.

5. `cp deploy/.env.example /srv/airbg/.env`, fill in every value (database
   credentials, `AIRBG_IMAGE_TAG`, `AIRBG_TILES_ARCHIVE`), then:

   ```bash
   chmod 600 /srv/airbg/.env
   chown root:root /srv/airbg/.env
   ```

6. Create the `collect` job's database credential file. `deploy/ofelia.ini`
   runs `collect` as a fresh container through the Docker API every 5
   minutes; that container inherits nothing from `app`'s `env_file: [.env]`,
   so `AIRBG_DATABASE_URL` has to reach it a different way — a bind-mounted
   file, the same pattern `/srv/airbg/pgpass` already uses for the backup job
   (§7). Put the *same* connection string as `AIRBG_DATABASE_URL` in
   `/srv/airbg/.env` on one line in `/srv/airbg/airbg_database_url`, then:

   ```bash
   chmod 600 /srv/airbg/airbg_database_url
   chown root:root /srv/airbg/airbg_database_url
   ```

   Skipping this step doesn't fail loudly at `docker compose up -d` — the
   long-lived services all start fine. It fails quietly, every 5 minutes,
   inside the `collect` job's own container: internal/config.Validate makes a
   missing credential fatal at startup, and `deploy/ofelia.ini` sets
   `delete = true` on that job, so there is nothing left in `docker ps` to
   show it happened. Check with the "collector has run" item in the §3
   checklist before you trust that the site is actually getting new data.

7. `cp deploy/docker-compose.prod.yml deploy/Caddyfile deploy/ofelia.ini /srv/airbg/`

8. Generate and install the tiles artefacts per `docs/tiles.md`, then set
   `AIRBG_TILES_ARCHIVE` in `/srv/airbg/.env` to match the exact filename you
   installed.

9. Build and load the image (see §4 Releases), then also tag it `latest`:

   ```bash
   docker tag airbg:$TAG airbg:latest
   ```

   This is required, not cosmetic: `deploy/ofelia.ini`'s `backup` job runs
   `image = timescale/timescaledb-ha:pg18`, unrelated to the app image, but
   its own `collect` job runs `image = airbg:latest` — ofelia's INI format has
   no way to interpolate the current release tag into a job definition, so
   `collect` always runs whatever `latest` currently points at. If you forget
   this step, the scheduled collector keeps running the previous release
   after every deploy.

10. `cd /srv/airbg && docker compose -f docker-compose.prod.yml run --rm app validate-config`

    Checks `airbg.yaml` plus the `.env` overlay before anything touches the
    database or opens a listener. Fix any reported error before continuing.
    (`docker compose` reads `.env` from the current directory automatically —
    that's why step 5 puts it at `/srv/airbg/.env`.)

11. `docker compose -f docker-compose.prod.yml run --rm app migrate`

12. `docker compose -f docker-compose.prod.yml run --rm app import-areas`

13. `docker compose -f docker-compose.prod.yml up -d`

## 3. Post-deploy checklist

Run every item below. Do not announce the site until all of them pass.

- The site answers through Cloudflare:

  ```bash
  curl -sS -o /dev/null -w '%{http_code}\n' https://airbg.org/
  # want: 200
  ```

- **The origin refuses a direct connection.** This is the assertion the whole
  design rests on: if this returns a page instead of a handshake failure,
  *stop and fix it before announcing the site* — it means Cloudflare's client
  certificate requirement is not actually being enforced, and anyone can
  bypass Cloudflare's rate limiting by hitting the origin IP directly.

  ```bash
  curl -sS --resolve airbg.org:443:<origin IP> https://airbg.org/
  # want: a TLS handshake failure, not a page
  ```

- Tiles answer directly with a browser-valid certificate (this vhost is
  DNS-only, so it must NOT require a client certificate):

  ```bash
  curl -sSI https://tiles.airbg.org/<archive name> | head -1
  # want: HTTP/2 200 (or 304), with a certificate curl trusts by default
  ```

- Metrics are not public:

  ```bash
  curl -sS --max-time 5 http://<origin IP>:9090/metrics
  # want: connection refused
  ```

- Metrics are readable internally, from a throwaway container on the same
  Docker network (the app image has no shell, so you can't `exec` into it):

  ```bash
  docker run --rm --network airbg_back curlimages/curl -sS http://app:9090/metrics | head -5
  ```

- The collector has run:

  ```bash
  docker logs airbg-ofelia-1 --since 10m 2>&1 | grep -i collect
  ```

  and the site itself shows recent sensor readings, not stale ones.

  Check `ofelia`'s own container, not the `collect` job's: `deploy/ofelia.ini`
  sets `delete = true` on the `collect` job, so it runs as a fresh container
  every 5 minutes (`schedule = @every 5m`) and is torn down immediately after
  — `docker ps` without `-a` only lists running containers, so outside the
  brief window the job actually executes, there is nothing for `docker ps` to
  find and `docker logs` on an empty substitution just errors with a usage
  message. `ofelia` itself is `restart: unless-stopped` in
  `deploy/docker-compose.prod.yml`, with no `container_name:` set, so Compose
  names it `<project>-<service>-<replica>` — `airbg-ofelia-1`, since the
  compose file's top-level `name:` is `airbg`. That container is long-lived
  and streams each job's output to its own stdout as it runs each job, so its
  logs are readable at any time, not just during the few seconds a job
  container exists — which is what makes this check survive the gap between
  runs.

## 4. Releases

Build on your workstation or CI host, ship the image over SSH, and update the
running stack in one sequence:

```bash
TAG=$(git rev-parse --short HEAD)
docker build -t airbg:$TAG .
docker save airbg:$TAG | gzip | ssh airbg 'gunzip | docker load'
ssh airbg "docker tag airbg:$TAG airbg:latest \
  && sed -i 's/^AIRBG_IMAGE_TAG=.*/AIRBG_IMAGE_TAG=$TAG/' /srv/airbg/.env \
  && cd /srv/airbg \
  && docker compose -f docker-compose.prod.yml run --rm app migrate \
  && docker compose -f docker-compose.prod.yml up -d"
```

`migrate` runs as a separate one-shot container, not as part of `app`'s
startup, deliberately: a migration failure must stop the deploy outright, not
crash-loop a serving container while `caddy` keeps sending it traffic.

Also re-tag `latest` on every release (step above) — see §2.8 for why
`ofelia`'s `collect` job depends on it.

Rollback is the same sequence run with the previous commit's short SHA in
place of `$TAG`: the previous image stays loaded on the host until something
prunes it, so no rebuild or re-transfer is needed to go back.

## 5. Development access

The app publishes no port, so there is no localhost URL to browse directly.
Reach it with an SSH local forward to its fixed container address instead —
these IPs are pinned (`ipv4_address:`) in `deploy/docker-compose.prod.yml`
precisely so this command has a stable target:

```bash
ssh -L 8080:172.28.0.10:8080 airbg    # browse http://localhost:8080
ssh -L 9090:172.29.0.10:9090 airbg    # metrics
```

Two caveats:

- `AIRBG_LISTEN_BASE_URL` is `https://airbg.org` in production, so canonical
  links, `hreflang` tags and the language switcher all point at production
  while you browse through the forward — relative navigation within the page
  behaves normally, but any absolute link takes you back to the live site.
- The forward bypasses both Caddy and Cloudflare, so it does not exercise
  `CF-Connecting-IP` bucketing, Caddy's headers, or the client-certificate
  requirement on the `airbg.org` vhost. Verify all of those against the
  public URL (§3), never against the forward.

No `ports:` line is ever added to `docker-compose.prod.yml`, or to any
per-operator override of it, to get local access — in any environment. Use
the forward above instead.

## 6. Tiles regeneration

Generating the basemap artefacts themselves — the `.pmtiles` archive, glyphs,
and `style.json` — is covered in `docs/tiles.md`; follow that document for the
build. Three names must agree, or the tiles listener refuses to start or
browsers fetch a 404:

1. The archive's filename on disk in `/var/lib/airbg/tiles/`.
2. `AIRBG_TILES_ARCHIVE` in `/srv/airbg/.env`.
3. The `pmtiles://` source URL embedded in `style.json`.

The filename **must change on every regeneration** — tiles are served with a
`Cache-Control: immutable` response, cached for a year, so reusing a filename
means returning visitors keep the old map baked into their browser cache with
no way to invalidate it.

## 7. Backups and restore

`ofelia`'s nightly `backup` job (03:30 UTC) writes
`/var/backups/airbg/airbg-YYYYMMDD.dump` using `pg_dump -Fc`. A separate
`backup-prune` job (03:45 UTC) deletes anything older than 14 days.

The backup job authenticates via a pgpass file, not an inline password:
`/srv/airbg/pgpass` is bind-mounted read-only into the job container. This
file **must exist on the host, be owned by `root`, and be `chmod 600`**,
containing one line in `hostname:port:database:username:password` format,
e.g. `db:5432:airbg:airbg:<password>`. `pg_dump` **silently ignores** a
pgpass file with looser permissions or a malformed line — the job then fails
with an authentication error that gives no hint the file itself is the
problem, so if a night's backup is missing, check this file's permissions and
contents before anything else.

The job writes to `/backups/.partial.dump` and only renames it to
`airbg-YYYYMMDD.dump` on success, so a failed run never overwrites the
previous night's good dump. A consequence: a stray `.partial.dump` left in
`/var/backups/airbg` is not a valid backup, is not covered by the 14-day
prune (it doesn't match the `airbg-*.dump` glob), and won't clean itself up.
Its presence is evidence the last run failed — delete it and investigate why.

Restore:

```bash
docker run --rm --network airbg_back -v /var/backups/airbg:/backups \
  -e PGPASSWORD=<password> timescale/timescaledb-ha:pg18 \
  pg_restore -h db -U airbg -d airbg --clean --if-exists /backups/airbg-YYYYMMDD.dump
```

This protects history, not availability: sensor readings already ingested
cannot be recovered from sensor.community after the fact (the upstream API
only serves recent data), even though future data can simply resume being
collected without a restore. Losing the database without a backup means that
history is gone permanently, even though the site itself would come back up
on the next `collect` cycle.

## 8. Adding a language or changing copy

Set `AIRBG_I18N_DIR` in `/srv/airbg/.env` to a host directory, drop a
`<lang>.json` file into it following the shape of the embedded catalogues,
and restart the `app` service. No image rebuild and no database migration is
needed — the catalogue is read from disk at startup. See
`docs/configuration.md` for the file format and validation rules.

## 9. Log rotation and disk

Every service in `deploy/docker-compose.prod.yml` uses the same `json-file`
logging driver with `max-size: "10m"` and `max-file: "5"` (the `journal`
anchor). This caps each service's logs at 50 MB total regardless of volume.

This is not just housekeeping: an unbounded log flood filling the disk is the
same class of denial of service the rate limiters exist to prevent, just
arriving through a different door — a full disk stops `db` from writing WAL,
stops the nightly backup from landing, and can wedge the host generally.

## Verifying the read-only, no-capability runtime

The `app` service runs `read_only: true` and `cap_drop: ["ALL"]`. Confirm the
image tolerates that before relying on it in production:

```bash
docker run --rm --read-only --cap-drop ALL airbg:latest validate-config
```

If this fails because something under the hood tries to write a temp file,
the fix is to add a small writable `tmpfs` mount to the `app` service in
`deploy/docker-compose.prod.yml` (`tmpfs: ["/tmp"]`) — **not** to relax
`read_only`. Dropping `read_only` reopens the filesystem the container's
security posture depends on staying closed; a scoped `tmpfs` mount does not.
