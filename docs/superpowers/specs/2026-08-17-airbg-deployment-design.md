# airbg.org deployment — design

**Date:** 2026-08-17
**Scope:** item 4 of the agreed order (1. cleanup, 2. Phase 2 API, 3. Phase 3 frontend, 4. deployment).
Items 1–3 are merged and pushed; this spec covers getting the merged tree onto a public host and
keeping it there.

## Goal

Run airbg.org on one rented Linux VPS with Docker Compose, such that:

- the application origin is reachable **only** through Cloudflare, structurally rather than by a
  firewall rule someone can forget;
- the whole system starts with `docker compose up -d` plus one root-owned `.env`;
- the ingest runs on a schedule, the database is backed up nightly, and an outage produces a
  notification;
- nothing new is compiled into the binary and no new Go dependency is added.

## What already exists

- `Dockerfile` — multi-stage `node:26-alpine` → `golang:1.26` → `gcr.io/distroless/static-debian13:nonroot`,
  ~27 MB, `USER nonroot`, no shell, `AIRBG_CONFIG=/etc/airbg/airbg.yaml` baked, `CMD ["serve"]`.
- Subcommands: `migrate`, `collect`, `serve`, `backfill`, `import-areas`, `purge-outside-boundary`,
  `validate-config`.
- `airbg.yaml` — mandatory, fully populated, no defaults in code. Every key overridable by
  `AIRBG_<KEY_PATH>`; list-valued keys take a comma-separated value (`internal/config/load.go:237`).
  `AIRBG_DATABASE_URL` is env-only and the only secret.
- Three listeners in the `serve` process: public (`listen.addr`), metrics (`listen.metrics_addr`),
  tiles (`tiles.addr`, all-or-nothing with `tiles.dir` / `public_url` / `archive`).
- `docker-compose.yml` — development only, states so in its header. It is **not** the production file
  and is not modified by this work.
- `docs/tiles.md` — basemap generation runbook, including two questions it explicitly deferred to
  this phase. Both are answered below.

## Topology

One VPS. Six containers, three Docker networks, **one published port on the entire box**.

| Service | Image | Networks | Published ports |
|---|---|---|---|
| `db` | `timescale/timescaledb-ha:pg18` | `back` | none |
| `app` | `airbg:<git-sha>`, `serve` | `back`, `edge` | none |
| `cloudflared` | `cloudflare/cloudflared` | `edge` | none — outbound only |
| `caddy` | `caddy` | `edge` | **80, 443** |
| `socket-proxy` | `tecnativa/docker-socket-proxy` | `sched` | none |
| `ofelia` | `mcuadros/ofelia` | `sched` | none |

### Why the app publishes no port

`cloudflared` dials **out** to Cloudflare and reaches `app:8080` across the `edge` network. There is
therefore no inbound application port to firewall, and discovering the origin IP yields nothing but
the tiles host.

This resolves the carried risk that the image's loopback default makes `-p 8080:8080` answer nothing.
Inside the container, `AIRBG_LISTEN_ADDR=0.0.0.0:8080` is correct **and** safe, because the container
publishes no port: the network namespace enforces the invariant that `listen.addr`'s comment was
approximating. The rule was never "never bind 0.0.0.0" — it was "never expose the origin".

`airbg.yaml`'s comment on `listen.addr` is updated to say this, so the next reader does not treat the
production override as the mistake the comment warns about.

### Trusted proxies

`listen.trusted_proxy_cidrs` must name **the `edge` network's subnet**, because the direct peer is the
`cloudflared` container — not Cloudflare's published ranges, which never appear as a peer address in
this topology. Trusting Cloudflare's ranges here would mean `CF-Connecting-IP` is ignored and every
visitor shares one rate-limit bucket.

The `edge` network is therefore declared with an explicit `ipam` subnet in the compose file, so the
CIDR in `.env` is deterministic rather than whatever Docker's default allocator picked that day.

### Metrics

`listen.metrics_addr` binds `0.0.0.0:9090` on the `back` network with no published port. The image
has no shell, so `docker exec` cannot read it; it is read by attaching a throwaway container to
`back`. It is unreachable from the internet, which is the design intent.

### Tiles

`tiles.dir` is a **bind-mounted host directory** (`/var/lib/airbg/tiles`), mounted read-only into
`app`. The image stays ~27 MB and regenerating the basemap is an scp, not a rebuild — which matters
because the release path ships the image over the wire (below).

`caddy` terminates TLS for `tiles.airbg.org` with automatic Let's Encrypt and proxies to `app:8082`.
The record stays **DNS-only** (grey cloud), as `docs/tiles.md` §6 specifies. Caddy renews on its own,
so there is no certificate timer to forget. A Cloudflare Origin CA certificate is explicitly rejected
here: it is trusted only by Cloudflare, and this hostname is deliberately not proxied.

This answers both of `docs/tiles.md`'s open questions — volume, not baked; own certificate via Caddy,
not a wildcard — and that section is rewritten to record the decisions rather than the options.

Note `docs/tiles.md` §7's egress ceiling still applies: `listen.max_conns` × archive size, served from
the origin's own bandwidth. Unchanged by this design; called out in the runbook when sizing the host.

### Scheduling

`ofelia` runs `airbg collect` on `upstream.poll_interval`'s cadence and `pg_dump` nightly, launching
each as a one-shot container from the same image.

Any in-compose scheduler needs the Docker socket, and that socket is root-equivalent on the host: a
container holding it can start a privileged container mounting `/`. So `ofelia` never sees the real
socket. `socket-proxy` holds `/var/run/docker.sock` read-only and grants exactly the endpoints
container creation needs (`CONTAINERS=1`, `POST=1`, everything else off, `ofelia` pointed at
`DOCKER_HOST=tcp://socket-proxy:2375`). The `sched` network carries only these two containers.

## Configuration and secrets

The baked `airbg.yaml` stays the base. Production differences ride as `AIRBG_*` variables from
`/srv/airbg/.env`, root-owned, `chmod 600`, never committed. No forked production YAML: a second
config file drifts silently the day a key is added or renamed.

The `.env` supplies at least:

| Variable | Value | Why it must be overridden |
|---|---|---|
| `AIRBG_DATABASE_URL` | `postgres://…@db:5432/airbg?sslmode=disable` | Env-only secret. `sslmode=disable` is acceptable only because the connection never leaves the compose network. |
| `AIRBG_LISTEN_ADDR` | `0.0.0.0:8080` | Reachable from `cloudflared`; safe because nothing is published. |
| `AIRBG_LISTEN_METRICS_ADDR` | `0.0.0.0:9090` | Same, on `back`. |
| `AIRBG_LISTEN_BASE_URL` | `https://airbg.org` | Canonical and `hreflang` URLs. |
| `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS` | the `edge` subnet | Otherwise every visitor shares one bucket. |
| `AIRBG_LISTEN_CSP` | shipped policy **plus `https://tiles.airbg.org` in `connect-src`** | Validation refuses to start with `tiles.public_url` absent from `connect-src`, because a CSP-blocked fetch is a blank map and no server-side error. |
| `AIRBG_TILES_ADDR` | `0.0.0.0:8082` | Reachable from `caddy`. |
| `AIRBG_TILES_DIR` | `/var/lib/airbg/tiles` | The read-only mount. |
| `AIRBG_TILES_PUBLIC_URL` | `https://tiles.airbg.org` | Must match the CSP entry above. |
| `AIRBG_TILES_ARCHIVE` | `bulgaria-YYYYMMDD.pmtiles` | Changes on every regeneration. |
| `TUNNEL_TOKEN` | Cloudflare tunnel token | Consumed by `cloudflared`, not by airbg. |
| `POSTGRES_USER` / `_PASSWORD` / `_DB` | — | Consumed by the `db` image. |
| `AIRBG_IMAGE_TAG` | git sha | What `app` and the scheduled one-shots run. |

`.env.example` in `deploy/` documents every one of these with the same reasons, carrying no real
values.

## Release path

Images are built on a workstation and shipped to the box; nothing is built on the VPS and no registry
is involved.

```
docker build -t airbg:$(git rev-parse --short HEAD) .
docker save airbg:<sha> | gzip | ssh airbg 'gunzip | docker load'
ssh airbg 'sed -i s/^AIRBG_IMAGE_TAG=.*/AIRBG_IMAGE_TAG=<sha>/ /srv/airbg/.env \
  && docker compose -f /srv/airbg/docker-compose.prod.yml run --rm app migrate \
  && docker compose -f /srv/airbg/docker-compose.prod.yml up -d'
```

The tag is the git sha, so "which commit is running" is answerable by `docker ps` alone. Rollback is
the same sequence with the previous sha, which stays loaded on the box until pruned.

`migrate` runs as a one-shot before `up -d`, not as an entrypoint step: a migration failure must stop
the deploy rather than crash-loop a serving container.

## Bootstrap

First-run sequence, documented as a numbered runbook:

1. Provision the host; install Docker; create `/srv/airbg` and `/var/lib/airbg/tiles`.
2. Write `/srv/airbg/.env` (`chmod 600`, root-owned) and the compose file.
3. Create the Cloudflare tunnel; put its token in `.env`; point `airbg.org` at the tunnel (proxied)
   and `tiles.airbg.org` at the host IP (DNS-only).
4. Generate and install the tiles artefacts per `docs/tiles.md`.
5. `docker compose run --rm app migrate`
6. `docker compose run --rm app import-areas` (and `purge-outside-boundary` if the source data needs it)
7. `docker compose up -d`
8. Verify: the site answers through Cloudflare; the origin IP answers **only** on the tiles host;
   `/metrics` is unreachable from outside; `docker compose run --rm app validate-config` is clean.

## Rate limit change

`ratelimit.enumerate.areas_per_window` ships as **30**, up from 12.

Bulgaria has 28 oblasti and comparing them is the site's obvious use, so at 12 a curious visitor's
13th area page returns `Retry-After: 900` — a UX failure hardening created and Phase 3 made
reachable. 30 covers the full set in one sitting plus a couple of repeats while still hard-stopping
sustained extraction: areas are a bounded set, and 30/hour is nothing like a bulk pull.
`sensors_per_window` is unchanged.

Two tests pin the old value and both are updated in the same commit as the change, never silently:

- `internal/config/resolve_test.go:28` — a plain shipped-value assertion.
- `internal/config/inert_test.go:105` — part of `TestShippedValuesMatchPhase2Behaviour`, whose whole
  purpose is proving the Phase 3b config sweep changed **no** behaviour. This is the first deliberate
  divergence from Phase 2 behaviour, so the row does not just get a new number: it gets a comment
  naming this spec and the reason, so the test keeps meaning "every difference from Phase 2 is
  accounted for" instead of degrading into "whatever the config currently says".

Per the project's standing rule, the retune is also explained in its commit message.

## Backups

`ofelia` runs `pg_dump -Fc` nightly into `/var/backups/airbg/airbg-YYYYMMDD.dump` on the host, with a
retention window that deletes dumps older than 14 days. The time-series data is regenerable from
sensor.community going forward, but the accumulated history is not, so this protects history rather
than availability.

The restore path is documented and exercised **once** during implementation against a scratch
database, because an untested restore is not a backup.

## Operations

Container logs go to the journal with size caps (`max-size`, `max-file`) so a log flood cannot fill
the disk — the same class of denial-of-service the rate limiters exist to prevent, arriving by a
different door.

A Cloudflare health-check notification emails when the origin stops answering. No Prometheus, no
Grafana, no external monitor: each would be another service to patch and access-control on a site
with no login, and `/metrics` is readable over SSH when a number is actually wanted.

## Files

| Path | Change |
|---|---|
| `deploy/docker-compose.prod.yml` | Create — the topology above. |
| `deploy/.env.example` | Create — every variable, documented, no real values. |
| `deploy/Caddyfile` | Create — `tiles.airbg.org` only. |
| `deploy/ofelia.ini` | Create — collect job, pg_dump job. |
| `deploy/compose_test.go` | Create — the invariant test below. |
| `airbg.yaml` | Modify — `areas_per_window` 12 → 30; `listen.addr` comment records why the production override is not the mistake it warns about. |
| `internal/config/resolve_test.go` | Modify — re-pin the changed value. |
| `internal/config/inert_test.go` | Modify — re-pin, with a comment recording the first deliberate divergence from Phase 2 behaviour. |
| `docs/deployment.md` | Create — bootstrap, release, rollback, tiles regeneration, backup/restore, incident basics. |
| `docs/tiles.md` | Modify — §"Open deployment questions" replaced by the decisions. |
| `docker-compose.yml` | Unchanged — development only. |
| `www-root/` | Unchanged, as always. |

## Testing

The deployment's security properties are file-level facts, which is exactly the shape that rots
silently. `deploy/compose_test.go` parses `docker-compose.prod.yml` with the YAML library already in
`go.mod` — **no new dependency** — and asserts:

1. `app` declares no `ports:` key. (A published app port destroys the Cloudflare-only invariant.)
2. `db` declares no `ports:` key and is not attached to `edge`.
3. `caddy` is the only service with published ports, and publishes only 80 and 443.
4. `app` is not attached to `sched`; `ofelia` and `socket-proxy` are not attached to `edge` or `back`.
5. `socket-proxy` sets `POST=1` and `CONTAINERS=1` and does not set any of the endpoint variables that
   would widen it (`EXEC`, `IMAGES`, `NETWORKS`, `VOLUMES`, `INFO`, `SWARM`, `SYSTEM`).
6. The `edge` network declares an explicit `ipam` subnet, and that subnet is the value
   `.env.example` documents for `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS`.
7. Only `ofelia` mounts anything named `docker.sock`, and does so read-only.

Every assertion is mutation-proven: the mutation that would make it pass while inert is run live and
shown to fail the test, per this project's standing rule that a test is not trusted until it has been
seen to fail.

The four existing tiers (`go test ./...`, `-tags integration`, Vitest, `-tags e2e`) must stay green;
`deploy/compose_test.go` joins the default tier.

## Out of scope

- CI publishing to a registry — the release path is deliberately manual.
- Any second environment (staging). One box, one environment.
- Moving tiles to R2 or behind the Cloudflare proxy — considered and declined; revisit only if origin
  bandwidth becomes a problem.
- `docker-compose.yml` (development) and `www-root/` (legacy PHP).
