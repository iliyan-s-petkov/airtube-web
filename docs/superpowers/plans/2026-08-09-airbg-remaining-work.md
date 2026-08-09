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
4. **Ship the authoritative Bulgaria boundary** (section B).
5. **Decide `.claude/`** — commit or gitignore. Currently untracked.
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
