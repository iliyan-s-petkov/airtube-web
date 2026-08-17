# airbg.org deployment — design

**Date:** 2026-08-17
**Scope:** item 4 of the agreed order (1. cleanup, 2. Phase 2 API, 3. Phase 3 frontend, 4. deployment).
Items 1–3 are merged and pushed; this spec covers getting the merged tree onto a public host and
keeping it there.

## Goal

Run airbg.org on one rented Linux VPS with Docker Compose, such that:

- the application origin serves **only** requests that came through Cloudflare, enforced
  cryptographically rather than by trusting a source address;
- no Cloudflare software runs on the VPS — the origin is a plain public HTTPS server, and the
  relationship with the CDN is a certificate, not a daemon;
- the whole system starts with `docker compose up -d` plus one root-owned `.env`;
- the ingest runs on a schedule, the database is backed up nightly, and an outage produces a
  notification;
- direct access to the application exists for development and is structurally absent in production;
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

One VPS. Five containers, three Docker networks, one service with published ports.

| Service | Image | Networks | Published ports |
|---|---|---|---|
| `caddy` | `caddy` | `edge` | **80, 443** |
| `app` | `airbg:<git-sha>`, `serve` | `back`, `edge` | none |
| `db` | `timescale/timescaledb-ha:pg18` | `back` | none |
| `socket-proxy` | `tecnativa/docker-socket-proxy` | `sched` | none |
| `ofelia` | `mcuadros/ofelia` | `sched` | none |

`caddy` is the only process the internet can open a socket to. It serves two vhosts:

- `airbg.org` — proxied by Cloudflare (orange cloud), **requires Cloudflare's client certificate**
  (below), proxies to `app:8080`.
- `tiles.airbg.org` — DNS-only (grey cloud) per `docs/tiles.md` §6, public, Let's Encrypt certificate,
  proxies to `app:8082`.

`app` publishes no port in any production file. It is reachable only from `caddy` across the `edge`
network and from `db` traffic across `back`.

### Enforcing "only via Cloudflare"

Cloudflare **Authenticated Origin Pulls**: Caddy's `airbg.org` vhost requires a TLS client
certificate issued by Cloudflare's origin-pull CA, which only Cloudflare's edge holds. A direct
connection to the origin IP on 443 with SNI `airbg.org` fails the TLS handshake — regardless of the
client's source address.

This is chosen over an nftables allowlist of Cloudflare's published ranges for two reasons. First,
the allowlist trusts *where a packet came from*, and a scraper needs only one machine inside those
ranges — a Worker, or any other customer's origin — to qualify. Second, `tiles.airbg.org` must accept
the world on the same port 443, and a packet filter cannot distinguish the two hostnames: SNI is
above the layer it operates at. The certificate requirement is per-vhost, so it separates them
exactly.

The `airbg.org` vhost presents a **Cloudflare Origin CA certificate** (15-year, trusted by Cloudflare,
not by browsers — correct here precisely because only Cloudflare ever connects). The
`tiles.airbg.org` vhost presents a normal Let's Encrypt certificate, obtained and renewed by Caddy,
because browsers connect to it directly.

`nftables` remains as an outer layer, not as the enforcement: default-drop inbound, permitting only
22, 80 and 443. It protects against something later binding a port by accident; it is not what keeps
scrapers off the API.

### Why the origin still cannot be bypassed

`CF-Connecting-IP` is the rate-limit bucket key, and it is attacker-controlled on a direct
connection. With Authenticated Origin Pulls, a direct connection to `airbg.org` never completes a
handshake, so no request with a forged header ever reaches the application. Discovering the origin IP
yields the tiles host and a TLS rejection.

The carried risk that the image's loopback default makes `-p 8080:8080` answer nothing is resolved,
not waived: inside the container `AIRBG_LISTEN_ADDR=0.0.0.0:8080` is correct **and** safe, because the
container publishes no port. The rule was never "never bind 0.0.0.0" — it was "never expose the
origin". `airbg.yaml`'s comment on `listen.addr` is updated to say so, so the next reader does not
mistake the production override for the mistake the comment warns about.

### Trusted proxies

`listen.trusted_proxy_cidrs` must name **the `edge` network's subnet**, because the direct peer is the
`caddy` container — not Cloudflare's published ranges, which never appear as a peer address in this
topology. Trusting Cloudflare's ranges here would mean `CF-Connecting-IP` is ignored and every visitor
shares one rate-limit bucket.

Caddy passes `CF-Connecting-IP` through unmodified. The `edge` network is declared with an explicit
`ipam` subnet so the CIDR in `.env` is deterministic rather than whatever Docker's default allocator
picked that day; that same pinning gives `app` a static address, which the development access path
below depends on.

### Metrics

`listen.metrics_addr` binds `0.0.0.0:9090` on the `back` network with no published port. The image
has no shell, so `docker exec` cannot read it; it is read by attaching a throwaway container to
`back`. It is unreachable from the internet, which is the design intent.

### Tiles

`tiles.dir` is a **bind-mounted host directory** (`/var/lib/airbg/tiles`), mounted read-only into
`app`. The image stays ~27 MB and regenerating the basemap is an scp, not a rebuild — which matters
because the release path ships the image over the wire (below).

This answers both of `docs/tiles.md`'s open questions — volume, not baked; its own Let's Encrypt
certificate via Caddy, not a wildcard, and not an Origin CA certificate (browsers do not trust one,
and this hostname is deliberately not proxied). That section is rewritten to record the decisions
rather than the options.

`docs/tiles.md` §7's egress ceiling still applies: `listen.max_conns` × archive size, served from the
origin's own bandwidth. Unchanged by this design; called out in the runbook when sizing the host.

### Scheduling

`ofelia` runs `airbg collect` on `upstream.poll_interval`'s cadence and `pg_dump` nightly, launching
each as a one-shot container from the same image.

Any in-compose scheduler needs the Docker socket, and that socket is root-equivalent on the host: a
container holding it can start a privileged container mounting `/`. So `ofelia` never sees the real
socket. `socket-proxy` holds `/var/run/docker.sock` read-only and grants exactly the endpoints
container creation needs (`CONTAINERS=1`, `POST=1`, everything else off, `ofelia` pointed at
`DOCKER_HOST=tcp://socket-proxy:2375`). The `sched` network carries only these two containers.

## Development and validation access

Direct access to the application exists, and no production file contains a line that grants it.

**On a workstation** — unchanged by this spec: `docker-compose.yml` (development), `go run
./cmd/airbg serve`, and the four test tiers, including the e2e tier that boots the whole stack and
drives Playwright.

**Against the VPS** — an SSH local forward to the container's static address:

```
ssh -L 8080:<app static IP on edge>:8080 airbg      # browse http://localhost:8080
ssh -L 9090:<app static IP on back>:9090 airbg      # /metrics
```

SSH forwards to any address the VPS itself can reach, and the Docker bridge is one of them. Nothing
is published, so there is no `ports:` line to accidentally ship — which is why this is preferred over
a dev-only compose override. It works before DNS exists, needs no certificate, and gives a real
browser against the real production stack for validating the map, the islands and the bundle.

Two honest caveats, both documented in the runbook:

- `AIRBG_LISTEN_BASE_URL` is `https://airbg.org`, so canonical, `hreflang` and language-switcher links
  point at production while you browse the forward. Relative navigation (map island, area links)
  behaves normally.
- The forward bypasses Caddy and Cloudflare, so it does **not** exercise `CF-Connecting-IP` bucketing,
  the security headers Caddy adds, or Authenticated Origin Pulls. Post-deploy verification of those
  is done against the public URL.

**On-box checks after a deploy** — a throwaway `curlimages/curl` container attached to `edge` or
`back` hits `app:8080` and `app:9090` directly; `docker compose run --rm app validate-config` proves
the configuration before anything serves. These are the only way into a distroless image, and they
are as much access as debugging needs.

## Configuration and secrets

The baked `airbg.yaml` stays the base. Production differences ride as `AIRBG_*` variables from
`/srv/airbg/.env`, root-owned, `chmod 600`, never committed. No forked production YAML: a second
config file drifts silently the day a key is added or renamed.

The `.env` supplies at least:

| Variable | Value | Why it must be overridden |
|---|---|---|
| `AIRBG_DATABASE_URL` | `postgres://…@db:5432/airbg?sslmode=disable` | Env-only secret. `sslmode=disable` is acceptable only because the connection never leaves the compose network. |
| `AIRBG_LISTEN_ADDR` | `0.0.0.0:8080` | Reachable from `caddy`; safe because nothing is published. |
| `AIRBG_LISTEN_METRICS_ADDR` | `0.0.0.0:9090` | Same, on `back`. |
| `AIRBG_LISTEN_BASE_URL` | `https://airbg.org` | Canonical and `hreflang` URLs. |
| `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS` | the `edge` subnet | Otherwise every visitor shares one bucket. |
| `AIRBG_LISTEN_CSP` | shipped policy **plus `https://tiles.airbg.org` in `connect-src`** | Validation refuses to start with `tiles.public_url` absent from `connect-src`, because a CSP-blocked fetch is a blank map and no server-side error. |
| `AIRBG_TILES_ADDR` | `0.0.0.0:8082` | Reachable from `caddy`. |
| `AIRBG_TILES_DIR` | `/var/lib/airbg/tiles` | The read-only mount. |
| `AIRBG_TILES_PUBLIC_URL` | `https://tiles.airbg.org` | Must match the CSP entry above. |
| `AIRBG_TILES_ARCHIVE` | `bulgaria-YYYYMMDD.pmtiles` | Changes on every regeneration. |
| `POSTGRES_USER` / `_PASSWORD` / `_DB` | — | Consumed by the `db` image. |
| `AIRBG_IMAGE_TAG` | git sha | What `app` and the scheduled one-shots run. |

The Cloudflare Origin CA certificate and key, and the origin-pull CA certificate Caddy verifies
clients against, are files under `/srv/airbg/tls/`, root-owned, `chmod 600`, mounted read-only into
`caddy`. They are not in the repo and not in `.env`.

`deploy/.env.example` documents every variable with the same reasons, carrying no real values.

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

1. Provision the host; install Docker; create `/srv/airbg`, `/srv/airbg/tls`, `/var/lib/airbg/tiles`.
2. `nftables`: default-drop inbound, permit 22, 80, 443.
3. Cloudflare: `airbg.org` proxied; `tiles.airbg.org` DNS-only pointing at the host IP.
4. Issue a Cloudflare Origin CA certificate for `airbg.org`; download Cloudflare's origin-pull CA
   certificate; place all three files in `/srv/airbg/tls`. Enable Authenticated Origin Pulls for the
   zone.
5. Write `/srv/airbg/.env` (`chmod 600`, root-owned) and the compose file.
6. Generate and install the tiles artefacts per `docs/tiles.md`.
7. `docker compose run --rm app validate-config`
8. `docker compose run --rm app migrate`
9. `docker compose run --rm app import-areas` (and `purge-outside-boundary` if the source data needs it)
10. `docker compose up -d`
11. Verify, and record the results in the runbook as the post-deploy checklist:
    - the site answers through Cloudflare;
    - `curl --resolve airbg.org:443:<origin IP> https://airbg.org/` **fails the TLS handshake**;
    - `tiles.airbg.org` answers directly with a browser-valid certificate;
    - `/metrics` is unreachable from outside and readable from a throwaway container on `back`;
    - the SSH forward reaches the app.

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
| `deploy/Caddyfile` | Create — the two vhosts, including `client_auth` on `airbg.org`. |
| `deploy/ofelia.ini` | Create — collect job, pg_dump job. |
| `deploy/nftables.conf` | Create — default-drop inbound, permit 22/80/443. |
| `deploy/compose_test.go` | Create — the invariant tests below. |
| `airbg.yaml` | Modify — `areas_per_window` 12 → 30; `listen.addr` comment records why the production override is not the mistake it warns about. |
| `internal/config/resolve_test.go` | Modify — re-pin the changed value. |
| `internal/config/inert_test.go` | Modify — re-pin, with a comment recording the first deliberate divergence from Phase 2 behaviour. |
| `docs/deployment.md` | Create — bootstrap, release, rollback, dev access, tiles regeneration, backup/restore, post-deploy checklist. |
| `docs/tiles.md` | Modify — §"Open deployment questions" replaced by the decisions; §6 updated for the mTLS enforcement. |
| `docker-compose.yml` | Unchanged — development only. |
| `www-root/` | Unchanged, as always. |

## Testing

The deployment's security properties are file-level facts, which is exactly the shape that rots
silently. `deploy/compose_test.go` parses `docker-compose.prod.yml` with the YAML library already in
`go.mod` — **no new dependency** — for assertions 1–7. Assertion 8 reads the `Caddyfile`, which has
its own syntax and no Go parser available under the no-new-dependency rule, so it is checked as
text: locate the `airbg.org` site block by its header line and require a `client_auth` directive
within it. That is weaker than parsing, and the mutation proof is correspondingly specific — the
mutation moves `client_auth` into the wrong site block, which a naive whole-file substring check
would not catch.

The assertions:

1. `app` declares no `ports:` key. (A published app port is a direct-to-origin entrance, which the
   whole design exists to deny.)
2. `db` declares no `ports:` key and is not attached to `edge`.
3. `caddy` is the only service with published ports, and publishes only 80 and 443.
4. `app` is not attached to `sched`; `ofelia` and `socket-proxy` are not attached to `edge` or `back`.
5. `socket-proxy` sets `POST=1` and `CONTAINERS=1` and does not set any of the endpoint variables that
   would widen it (`EXEC`, `IMAGES`, `NETWORKS`, `VOLUMES`, `INFO`, `SWARM`, `SYSTEM`).
6. The `edge` network declares an explicit `ipam` subnet, and that subnet is the value
   `.env.example` documents for `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS`.
7. Only `ofelia` mounts anything named `docker.sock`, and does so read-only.
8. The `Caddyfile`'s `airbg.org` vhost contains a `client_auth` block requiring verification, and the
   `tiles.airbg.org` vhost does not. (Assertion 8 is the one that would silently disappear during an
   ordinary Caddyfile edit and take the entire enforcement with it.)

Every assertion is mutation-proven: the mutation that would make it pass while inert is run live and
shown to fail the test, per this project's standing rule that a test is not trusted until it has been
seen to fail.

The four existing tiers (`go test ./...`, `-tags integration`, Vitest, `-tags e2e`) must stay green;
`deploy/compose_test.go` joins the default tier.

## Out of scope

- `cloudflared` and Cloudflare Tunnel — considered and rejected: it puts a vendor daemon with a
  routing token on the VPS. The origin is a plain HTTPS server instead.
- A dev-only compose override publishing the app port — rejected in favour of the SSH forward, so no
  file in the repo ever contains a line that exposes the origin.
- CI publishing to a registry — the release path is deliberately manual.
- Any second environment (staging). One box, one environment.
- Moving tiles to R2 or behind the Cloudflare proxy — considered and declined; revisit only if origin
  bandwidth becomes a problem.
- `docker-compose.yml` (development) and `www-root/` (legacy PHP).
