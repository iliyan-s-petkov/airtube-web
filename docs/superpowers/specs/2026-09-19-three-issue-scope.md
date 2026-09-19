# Scope and task plan — 19 Sep 2026

Three requests: prod deploy automation, `/api/v1/scales` duplicates, replay digit flicker.

## A. Replay digits blinking — root cause CONFIRMED

Not a rendering defect. A cell with no reading for an hour keeps its no-data grey
fill (`web/src/islands/map.js:296`) while the hex label filter
`['!=', ['get','value'], null]` drops its digit. The hexagon stays and the number
disappears; different cells drop out in different hours, so digits look like they
move around at random.

Measured on production P2/24h:

| tier | cells | always present | intermittent | blink events |
|---|---|---|---|---|
| 50 km | 140 | 136 | 4 (2.9%) | 11 |
| 25 km | 227 | 214 | 13 (5.7%) | 30 |
| 15 km | 285 | 269 | 16 (5.6%) | 32 |
| 5 km | 432 | 401 | 31 (7.2%) | 62 |

Ruled out by measurement in an isolated MapLibre harness driving the real payload:

- **Collision thinning** — churn 11 with `text-allow-overlap:false`, 13 with it
  true, identical placed counts. No reshuffling.
- **Fade (`fadeDuration` 300 ms vs `FRAME_MS` 320 ms)** — partial-opacity symbol
  count was 0 at every mid-interval sample (90 ms and 180 ms after each frame).

Commit `096fc5a` fixed whole missing tiers; the minimum-coverage guard (`78b60c9`)
captions whole thin frames. Neither addresses a single cell dropping out of a
frame that is otherwise healthy.

### The open decision (product, not technical)

What a cell with no reading for one hour should show during replay. Three options:

1. **Carry the last reading forward** within the span, drawn in a muted style.
   Keeps the number stable; risks implying a reading that was never taken.
2. **Hold the cell's colour and digit from the previous frame** only for cells
   that are otherwise continuous, dropping out only after N consecutive gaps.
   Same risk, bounded.
3. **Leave it as-is and explain it** — the honest option. The blink IS the data.
   A legend note plus per-cell styling that makes "no reading this hour" read as
   deliberate rather than broken.

Option 3 is the only one that does not put a number on screen for an hour nobody
measured, which is the standard the rest of this codebase holds to
(`frameBody`'s "No `n`: a frame carries no sensor count, and a made-up one would
be a popup saying so"). Recommend 3, but it is the user's call.

## B. `/api/v1/scales` duplicate metric entries — NOT accidental duplication

`Scales()` (`internal/api/scales.go:100-141`) legitimately defines three scales
each for P1 and P2: EAQI, EU limit, and WHO guideline. They differ in `Name`,
`Bands`, `Notes`, and `Source`.

The defect is in the contract, not the data: the response is an array keyed only
by `metric`, which is not unique, while every consumer does a first-match
`.find(s => s.metric === metric)`:

- `bandsFor` — `web/src/islands/map.js:1643`
- `scaleFor` — `web/src/lib/scaleinfo.js:14`
- `hasScale` / `unitFor` — `web/src/lib/metrics.js:20`

Because EAQI is emitted first, P1/P2 always resolve to EAQI and the EU-limit and
WHO tables are unreachable dead payload — shipped over the wire, never rendered,
with no UI to select them.

Fix belongs server-side, and depends on a product decision: either mark exactly
one scale per metric as the default and add an explicit selector to the wire
format, or drop the unreachable entries. Do **not** patch `.find()` to `.filter()`
client-side without deciding first whether EU-limit/WHO views should ever be
user-selectable.

## C. Prod deployment via Ansible — already done; I was wrong earlier

I previously told the user that `tools/deploy-airbg.sh` targets staging and prod
uses a separate manual path. That is incorrect. Correcting it here:

`tools/deploy-airbg.sh` is a shim that pulls three Infisical credentials from the
macOS keychain (keeping them out of shell history) then execs
`ansible-playbook automations/ansible_collections/home/apps/playbooks/airbg.yml`.
Ansible content lives in the sibling repo `~/Work/home/automation/ansible`.

Inventory `inventory/home/00-static.yml` defines `airbg_hosts` as `tag_airbg`
(staging, Proxmox-tag-derived, vm 206) plus `airbg_prod` (static, 89.252.247.210).
Role `home.apps.airbg` runs preflight → host baseline → deploy artefacts →
secrets → certificate → image → stack. Environment differences live in group_vars;
`airbg_open_origin: true` is set in host_vars for the staging guest only, so it
cannot leak to prod.

**So prod deploy is already Ansible.** The real gap is upstream: no CI workflow
builds or publishes an image (`.github/workflows/ci.yml` only tests and builds).
That is why my own deploys this session were manual `docker build` → `docker save`
→ `scp` → `docker load` → edit `AIRBG_IMAGE_TAG` → restart. The automation worth
adding is **image build-and-publish**, not a deploy rewrite.

## Tasks and model assignments

| # | Task | Model | Why |
|---|---|---|---|
| A1 | Await the user's choice among the three replay options | — | Product decision |
| A2 | Implement the chosen option, tests first, in `web/src/islands/map.js` + `web/src/lib/timelapse.js` | opus | Touches the hex label filter and the replay path; the regression history here is mine |
| A3 | Copy for the chosen option in `internal/i18n/{en,bg}.json` + `base.gohtml` wiring | haiku | Mechanical, pattern already established by the coverage guard |
| B1 | Decide default-vs-drop for the P1/P2 scale variants | — | Product decision |
| B2 | Server-side change in `internal/api/scales.go` + handler tests | sonnet | Scoped, single file, clear contract |
| B3 | Client selector change across the three first-match consumers, only if B1 chooses a selector | sonnet | Three small call sites, needs consistency |
| C1 | CI workflow to build and publish the image on tag/master | sonnet | Well-scoped YAML against an existing workflow |
| C2 | Point the Ansible role's `tasks/image.yml` at the published image | sonnet | Sibling repo; role edits already authorized |

Review, mutation testing, commits and deploys stay with me.

## Carried risks

- The raw community backfill begins expiring around 30 Sep 2026 (30-day retention
  on `reading`); the `reading_hourly` seed is durable for 2 years.
- NOX captions 19 of 24 frames under the new coverage guard — honest but may read
  as broken. A per-metric floor would be the fix, not a threshold change.
