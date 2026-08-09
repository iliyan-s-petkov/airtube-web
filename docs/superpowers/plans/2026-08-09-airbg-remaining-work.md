# airbg.org — remaining work after Phase 1

**Status:** working checklist, not an SDD plan. Phase 2 and Phase 3 get their own
spec → plan cycle; this document tracks what stands between here and a merged,
deployable Phase 1, plus the decisions Phase 2 depends on.

**Date:** 2026-08-09

---

## A. Close out Phase 1

1. **Fix wave** (in flight) — 1 Critical, 7 Important, 5 ledger items from the final
   whole-branch review. Report will land at `final-fix-report.md`.
2. **Scoped re-review** of the fix diff only, BASE `d98a9bc`. Verifies each finding is
   genuinely addressed and the fix introduced nothing new. One round — this is the last
   gate, not a new loop.
3. **Adjudicate residuals.** Anything the re-review leaves open gets a ruling recorded
   in the ledger. A load-bearing residual stops the merge and goes to the owner.
4. ~~**Ship the authoritative Bulgaria boundary**~~ — done, commit `8042c98`. See section B.
5. **Apply the queued tag and version bumps** (section F). Blocked on the fix wave only
   because it holds the same files and is running the test suite.
6. **Decide `.claude/`** — commit or gitignore. Currently untracked.
6. **Delete the SDD workspace** `.superpowers/sdd/2026-08-07-airbg-phase1-data-foundation/`.
   Git-ignored scratch; its value is spent once the branch merges.
7. **Merge** via `superpowers:finishing-a-development-branch` — PR or direct merge of
   `feat/phase1-data-foundation` into `master`, owner's choice.

## B. Authoritative Bulgaria boundary

The collector fails closed without a `country`-kind boundary, and the only polygon in
the repo is a hand-authored 22-vertex test fixture, materially wrong along the eastern
border (max lon 28.00 against Bulgaria's real ~28.6 — it would silently drop Balchik,
Kavarna and Shabla). It is test-only and never loaded at runtime.

Source **Natural Earth 1:10m Admin 0** — public domain, so it can be committed rather
than left as an operator burden. Extract Bulgaria alone, store it as a data file, and
document the one-time `import-areas <path> country` step in the README. This removes
the sharpest operational trap in Phase 1: a deployment that looks healthy and stores
nothing.

Keep the test fixture as-is. It is deliberately crude, its inaccuracy is documented,
and tests that assert "just outside the boundary" depend on knowing its exact shape.

## C. Inherited legacy secret — closed, no action

`www-root/lib/geo2addr.class.php:39` carries a Google Maps API key in committed
history. The owner inherited this codebase and cannot revoke the key; it is not theirs.
Decided 2026-08-09: **accept and contain.** `www-root/` is excluded from the Docker
build context, the Go binary never reads it, and nothing in the rewrite depends on
Google Geocoding. The key ships nowhere. Do not raise this again.

## F. Queued tag and version bumps

All four are decided and verified; none is applied yet. They are held only because the
final-review fix wave holds the same files and is running the container suite.

**1. Distroless base — `static-debian12` → `static-debian13`.** Edited, not committed.
debian13 (trixie) is current stable; debian12 is oldstable. The binary is statically
linked (`CGO_ENABLED=0`), so the base contributes only CA certificates, `/etc/passwd`
and tzdata — which still carry CVE fixes. Base verified to pull (uid 65532, 819 KB).
**Outstanding:** end-to-end `docker build` and a `--help` run, once the tree compiles.

**2. GitHub Actions — `checkout@v4` → `@v7`, `setup-go@v5` → `@v7`.** Not started. The
fix wave is editing `.github/workflows/ci.yml` to put `TestUpstreamContractLive` on a
schedule, so this waits to avoid a conflict.

**3. PostgreSQL 16 → 18.** Owner decision, 2026-08-09. This **supersedes the approved
spec's PostgreSQL 16**. Rationale: support runway to 2030, and the re-verification cost
only grows once there is production data. Verified against
`timescale/timescaledb-ha:pg18` before committing to the choice:

| Check | Result |
|---|---|
| Server | 18.4 |
| PostGIS | 3.6.4 (`USE_GEOS=1 USE_PROJ=1`) |
| TimescaleDB | 2.29.1 |
| `create_hypertable` | works |
| `add_retention_policy` | works, job registered |
| `geography` readback | lon 23.3219 / lat 42.6977 — order preserved |
| `ST_Covers`, correct-order envelope | true |
| `ST_Covers`, swapped envelope | false |

The last two matter most: they are the property the coordinate-order guards assert, and
they hold on pg18.

Sites to change: `docker-compose.yml`, `internal/testsupport/postgres.go:23`, plus the
PostgreSQL 16 references in `README.md` and the Phase 1 spec (note the supersession
rather than rewriting history). **Outstanding:** a full `go test ./... -race` against
pg18 — migrations in order on a fresh database, hypertables, both retention policies,
and the boundary/`ST_Covers` paths. Not a formality; this is the whole cost of the bump.

**4. Current and deliberately unchanged:** `golang:1.26` and CI `go-version: '1.26'`
resolve to go1.26.5, the current stable, image rebuilt 2026-08-05.

## D. Phase 2 — the API layer

Needs its own brainstorm → spec → plan cycle. Requirements already stated by the owner,
to carry into that conversation:

- An API, but **not public**. A partner/consumer API, not an open endpoint.
- No login or user accounts initially.
- Protection against bulk extraction — "somebody trying to get all the information at
  once through a public GET".
- Rate limiting, explicitly to stop scrapers and brute-force enumeration.
- Very high security generally, including denial-of-service resistance.

Open questions for that brainstorm (ask one at a time):
- What does "not public" mean concretely — API keys, mTLS, IP allowlist, signed URLs?
- What does the map frontend consume? If it hits the same API, the anti-bulk-extraction
  requirement and the frontend's need for a viewport snapshot are in direct tension.
- Snapshot granularity: pre-aggregated tiles/snapshots, or live queries per viewport?
- Is historical data exposed at all, or only current values plus short history?

## E. Phase 3 — frontend

Also its own cycle. Frontend (Svelte islands per the Phase 1 spec), self-hosted PMTiles,
charts, i18n (Bulgarian and English). Blocked on Phase 2's API shape.

## Constraints that carry forward to every phase

- Module path exactly `airbg.org`. Deps limited to pgx/v5, goose/v3,
  testcontainers-go, stdlib.
- All SQL parameterised. String-concatenated SQL forbidden project-wide, tests
  included — the legacy app's InfluxQL injection hole is a stated reason this rewrite
  exists.
- No secrets in the repo; configuration from environment variables only.
- Canonical metrics: `P1` (PM10), `P2` (PM2.5), `temperature`, `humidity`, `pressure`
  (hPa, 650–1100), `noise_LAeq`, `noise_LA_max`.
- PostGIS `geography` is (longitude, latitude) — inverse of the legacy `[lat, long]`.
- `www-root/` is untouched legacy. It is not deployed.
- `CLAUDE.md` is gitignored deliberately and never staged. No `Co-Authored-By` trailer
  and no "Generated with Claude Code" line in any commit or PR body.
