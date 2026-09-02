# airbg.org — OpenProject seed

Content for the OpenProject project, ready to push once the MCP server can write
to it. Structure: eight phase epics (history), one decision-record epic, and the
open backlog.

Sources: the six SDD ledgers under `.superpowers/sdd/`, the specs and plans under
`docs/superpowers/`, and `git log` on `master`.

---

## Project

- **Name:** airbg.org
- **Identifier:** `airbg-org`
- **Public:** no

Free air-quality map for Bulgaria, rebuilt from the legacy PHP app "Dusty Map"
(airtube-web / airbg.info), using sensor.community data. Single static Go binary
(module path `airbg.org`), PostgreSQL 16 + PostGIS + TimescaleDB, Svelte 5 islands
with MapLibre GL, self-hosted Protomaps PMTiles basemap, deployed as a single-VPS
Docker Compose stack behind Cloudflare.

Standing constraints: no secrets in the repo; all SQL through pgx parameterised
queries; no new third-party dependencies; `www-root/` untouched. Anti-extraction is
tiering, not authentication.

---

## Epics — completed work (status: Closed)

Each epic's description carries its merge commit and the test evidence recorded on
the merged result, so the history is auditable without the ledgers.

### E1 — Phase 1: foundation, ingest and quality scoring
Go binary, Postgres/PostGIS/TimescaleDB schema, sensor.community ingest, quality
scoring, area boundaries and assignment. `ST_Covers` boundary filter with a distinct
`country` area kind; `purge-outside-boundary` refuses to run when no boundary exists,
so "no boundary" can never mean "delete everything".

### E2 — Phase 2: JSON API
Merged `43e1bf1`, 31 commits. Middleware chain, in-memory published snapshot,
hand-rolled metrics, server-rendered BG/EN area pages. Ledger:
`.superpowers/sdd/2026-08-09-airbg-phase2-api/progress.md`.

### E3 — Hardening (unplanned, owner-requested)
Merged `721de29`, 12 commits. Snapshot-served default series, non-blocking admission
semaphore, 5s scoped statement timeout, public-listener connection cap, two security
headers. Ledger: `.superpowers/sdd/2026-08-11-airbg-hardening/progress.md`.

### E4 — Phase 3a: frontend
Merged `a007f05` (`--no-ff`), 8 tasks. Svelte 5 islands, MapLibre GL, uPlot 24h chart,
Vite build, multi-stage `node:26-alpine` → distroless image, 26.6 MB. Green on merge:
17 packages ok default and `-tags=integration`, Vitest 67/67.
Ledger: `.superpowers/sdd/2026-08-11-airbg-phase3a-frontend/progress.md` (kept — sole
record of ten follow-ups plus the hardening ledger's H-A…H-G).

### E5 — Phase 3b: configuration sweep
Merged `18b4b65` (`--no-ff`), 37 commits `f1edc6a..5829a41`, 18 tasks plus a final fix
wave. Moved every hardcoded constant out of Go and JS into a committed `airbg.yaml`
plus `AIRBG_*` overrides. No defaults are compiled into the binary — a missing key is
a startup error — and `AIRBG_CONFIG` is mandatory with no fallback path. Green on
merge: 18 packages ok at both tag sets, Vitest 85/85.

The final whole-branch review found two config keys that existed but nothing read
(`ratelimit.api.retry_after`, deleted; `ratelimit.series.evict_interval`, wired), a
`.env.example` still documenting the pre-sweep world, and `.env` missing from
`.dockerignore`. All four fixed before merge.

### E6 — Self-hosted basemap (PMTiles)
Merged `bc83fdc` (`--no-ff`), 6 tasks, 22 commits. Deleted `AIRBG_BASEMAP_KEY` and
`basemap.style_url`; replaced the vendor basemap with a Protomaps PMTiles archive on
a third listener (`tiles.*`, all-or-nothing). `AIRBG_DATABASE_URL` is now the only
secret. The tiles listener holds no DB pool, snapshot, limiter or semaphore, but IS
connection-capped via `serveCapped` — file descriptors are process-wide, which was the
review's one Critical. Runbook `docs/tiles.md`. Green: 19 packages ok, Vitest 91/91.

### E7 — Phase 3c: frontend interaction
Merged `42eab1c` (`--no-ff`), 24 commits, 14 tasks plus a final fix round. Metric
switcher, sensor panel with `#sensor=` hash, find-me button, Playwright E2E. Green:
18 packages ok, Vitest 191/191, Playwright 13/13. Also added four-tier CI
(`.github/workflows/ci.yml`) and `internal/web/template_keys_test.go`, which requires
every template key to exist in **both** catalogues — `T` falls back through
`DefaultLang`, so a key missing only from `en.json` renders Bulgarian on an English page.

### E8 — Deployment
Merged `b3ad3e8` (`--no-ff`), 15 commits, 8 planned tasks plus three unplanned repairs.
Single-VPS Docker Compose stack: Caddy is the only container publishing ports (80/443),
the app publishes none. Secrets stay out of the repo via root-owned `chmod 600` host
files (`/srv/airbg/pgpass`, `AIRBG_DATABASE_URL_FILE`; env wins over file, and
missing/empty/unreadable is a hard error). Runbook `docs/deployment.md`. Green at all
three tag sets, Vitest 194/194.

---

## Epic — decision record (status: Closed, kept for history)

### D1 — Cloudflare-only origin is enforced at the TLS layer, not by IP allowlist
Authenticated Origin Pulls (`client_auth` + `require_and_verify`) on the `airbg.org`
vhost only. An IP allowlist was **rejected**: `tiles.airbg.org` must accept the public
internet on the same port 443, and a packet filter cannot distinguish hostnames because
SNI is above its layer. Do not "restore" an allowlist.

### D2 — `listen.trusted_proxy_cidrs` is the edge Docker subnet, never Cloudflare's ranges
`172.28.0.0/24` — Caddy is the direct peer. Backwards makes every visitor share one
rate-limit bucket. `CF-Connecting-IP` is honoured only when the socket peer is inside a
trusted CIDR, and a comma in the value means it did not come from Cloudflare and is
rejected. The default is empty: trust nobody.

### D3 — `permissions_policy` is `geolocation=(self)`, not `geolocation=()`
`()` is an **empty allowlist** that blocks the top-level document itself, so the site's
own header was disabling its own find-me button. Every other directive stays denied, and
the whole string is pinned by equality (deliberately not `strings.Contains`, which still
passes when a permissive directive is appended).

### D4 — Enumeration breadth: 12 → 30 → 20 areas per hour
Owner's call, 2026-08-18. The corpus is ~80 area pages, so 30/hour swept it in under
three hours; 20 keeps a 28-oblast comparison session comfortable. Breadth limiting
**slows** extraction, it does not prevent it — do not describe it as anti-scraping
protection.

### D5 — ofelia `job-run` containers are attached to Docker's default bridge
So `internal: true` does **not** deny them egress. Proven with live containers after two
reviews reached opposite conclusions. Compose-managed services (`app`, `db`) are not
bridge-attached, so the property is real for them — that asymmetry is what made both
results look true. ofelia also keeps exactly one network per job (a second `network =`
replaces the first). Re-check on any ofelia image bump; the topology leans on
undocumented behaviour.

### D6 — Anti-extraction is tiering, not authentication
There is no login and no user data. No endpoint accepts a bounding box or an unbounded
list parameter; bulk extraction requires enumerating areas, caught by counting *distinct*
slugs/sensor IDs per IP prefix. Repeats are deliberately free.

### D7 — `serve` runs one process, two pools, and separate listeners
The snapshot lives in process memory, so poller and server share an address space. Two
pools are a bulkhead: while both workloads shared one pool, request handlers blocked
behind the poll cycle on a schedule, and every control in place saw a healthy system
because it was one. `/metrics` is a separate listener, not a path prefix — a prefix is one
routing mistake away from exposing the counters that tell a scraper whether it is being
throttled.

### D8 — `reading_hourly` is a plain hypertable, not a continuous aggregate
`internal/backfill` writes into it directly. Retention: raw `reading` 30 days,
`reading_hourly` two years.

### D9 — The inertness pin is the proof the config sweep changed nothing
`internal/config/inert_test.go` pins ~60 shipped values against the constants they
replaced, with the `want` column recovered from the pre-sweep tree (which has no
`airbg.yaml`, so the values could not have been copied from it). Retuning a value later is
legitimate — say why in the commit message rather than editing the test silently.

### D10 — The inherited Google Maps API key is closed
Owner's ruling: *"Forget about the google maps api key. It isn't mine, I've inherited that
code base so I can't change it."* The key in the legacy PHP app is not to be raised again,
and is not a blocker on pushing.

### D11 — Pressure floor is 650 hPa, superseding the spec's 800
Musala is 2925 m. A sensor at altitude reads below 800 hPa legitimately, and the spec's
floor would have scored it `out_of_range`.

### D12 — PostGIS `geography` is (longitude, latitude)
Inverse of the legacy `[lat, long]`. Bulgaria spans lon 22.3–28.7, lat 41.2–44.3, so the
ranges overlap and a swap is only detectable by asserting each axis against its own range.

---

## Open backlog (status: New)

### B1 — `staticcheck` and `govulncheck` run untagged, blind to `internal/e2e` *(Task)*
`.github/workflows/ci.yml:17-18` runs both without build tags, so files behind
`//go:build e2e` and `//go:build integration` are never analysed. Phase 3c closed the
same gap for `go vet` by running it at both tag sets; these two were left behind.

### B2 — `cmd/airbg/testdata/invalid-ratelimit.yaml` is a near-copy of `airbg.yaml` *(Task)*
286 lines against `airbg.yaml`'s 360 — already drifted. Every key added or removed forces
a regeneration or the test fails confusingly. Generate the fixture at test time from the
committed config instead of maintaining a copy.

### B3 — Triage the carried deferred minors *(Task)*
Roughly 90 deferred/follow-up entries across six kept ledgers, each ruled "carry" at the
time by a whole-branch review. Most are report-accuracy notes with no code defect; the
ledgers are the only record. Worth one pass to promote anything that has since become
load-bearing.
Ledgers: `2026-08-09-airbg-phase2-api`, `2026-08-11-airbg-hardening`,
`2026-08-11-airbg-phase3a-frontend`, `2026-08-15-airbg-basemap-pmtiles`,
`2026-08-16-airbg-phase3c-frontend`, `2026-08-17-airbg-deployment`.

### B4 — No production-grade national boundary ships with the repo *(Task)*
`import-areas <file.geojson> country` is a hard prerequisite: until it has run, `collect`
fails closed — it polls upstream successfully and stores zero rows every cycle, logging an
ERROR that names the remedy. Nothing else looks unusual, so this is easy to miss. Source
and commit a boundary fit for production, or document where the operator obtains one.

### B5 — Production cutover: serve airbg.org from the VPS *(Feature)*
The stated goal of the whole rewrite — host it again as a free community air-quality map.
Everything needed is merged; this is the go-live itself: provision the VPS, DNS, the
Cloudflare origin certificate and Authenticated Origin Pulls, the PMTiles archive, the
boundary import (B4 is a prerequisite), and the first `collect` run.

---

## Boards

1. **Backlog** — status open, grouped by type, sorted by priority.
2. **Delivery history** — status Closed, grouped by type, sorted by ID; the audit trail of
   what shipped and when.
3. **Decisions** — the D-series epic's children, so a decision can be found without
   reading six ledgers.
