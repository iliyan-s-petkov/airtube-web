# Why the design kit lives in this repo

The kit is the design contract for airbg.org: `DESIGN.md`, the tokens, and a
clickable prototype of every screen. The app serves it at `/design-kit/` when
`design_kit.dir` points here.

It is authored elsewhere — in an OpenDesign project directory under
`~/Library/Application Support/`, which the editor writes to in place. That
directory is not a deployable source: a host cannot pull from a laptop path, and
an rsync someone remembers to run is not a deploy step. The first time it is
forgotten, `/design-kit/` is silently stale while still rendering correctly.

So the kit was imported here, and **this copy is now the source of truth** —
edited directly, and committed like any other part of the app. The host only
ever receives what was committed.

`tools/sync-design-kit.sh` was the import path. It runs the wrong way now, with
`--delete`, so it refuses unless `SYNC_FROM_EDITOR=1` is set. Use it only for a
deliberate re-import, and read `git status` before committing the result.

## Shipping a change

    go test ./internal/server/ ./internal/designkit/
    git add design-kit && git commit
    tools/deploy-airbg.sh

The deploy builds the image from `git archive HEAD` and refuses outright if the
working tree is dirty — so committing is the gate, not a person. Nothing left
uncommitted can reach the host, and a passing local page proves nothing about
what is being served.

**https://airbg.org/design-kit/ no longer loads from the LAN.** As of
2026-09-03 the origin requires Cloudflare's client certificate, so a direct
connection to the host fails the TLS handshake — see
`docs/backlog/origin-is-open-and-enumeration-mutations.md` for why it accepted
anyone until then. Nothing about the kit changed and the route is still served;
it is just no longer reachable from here.

Review it locally instead, which is strictly better than the host ever was —
this serves the **working tree**, so uncommitted work is visible, while the host
only ever showed what was committed and deployed:

    docker compose up -d db
    export AIRBG_DATABASE_URL='postgres://airbg:airbg@localhost:5432/airbg?sslmode=disable'
    export AIRBG_CONFIG="$PWD/airbg.yaml"
    export AIRBG_DESIGN_KIT_DIR="$PWD/design-kit"
    go run ./cmd/airbg serve      # http://localhost:8080/design-kit/

No secret store and no certificate: `docker-compose.yml` is development-only and
every credential in it has a development fallback. `AIRBG_DESIGN_KIT_DIR`
overrides `design_kit.dir`, whose shipped value is the in-image path — see
`docs/configuration.md` for the `AIRBG_*` naming rule. `migrate` must have run
once against that database; `import-areas` is not needed for the kit route.

`file://` also renders the kit, but the tiles stand down and the SVG basemap
draws in their place — that validates layout and not the basemap integration.
The SSH forward in `docs/deployment.md` reaches the deployed app directly,
bypassing Caddy, if what you need is specifically the host's copy.

## What the app actually serves

`internal/designkit` allowlists five entries at the root and refuses everything
else:

    ui_kits/  assets/  tokens.css  colors_and_type.css  components.css

`design_kit.dir` must be THIS directory, not `ui_kits/` — `ui_kits/app/index.html`
loads `../../tokens.css`, which resolves here. Pointing it one level deeper
serves the page with every stylesheet missing, which renders as an unstyled kit
rather than an error.

Adding a sixth served root means editing `allowedRoots` in that package. It does
not happen by putting a file here.

## What the sync drops

Editor output rather than anything anyone wrote: `.file-versions/` (per-file
revision history, redundant once this is in git), `node-compile-cache/`,
`context/`, `preview/` scratch, `*.artifact.json`, working screenshots and
`image*.png`. None of it is reachable through the allowlist. `CLAUDE.md` is
dropped too, matching this repo's rule that agent instructions stay local.

The sync uses `--delete`, so a file the designer removed disappears here as well.
