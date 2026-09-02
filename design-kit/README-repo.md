# Why the design kit lives in this repo

The kit is the design contract for airbg.org: `DESIGN.md`, the tokens, and a
clickable prototype of every screen. The app serves it at `/design-kit/` when
`design_kit.dir` points here.

It is authored elsewhere — in an OpenDesign project directory under
`~/Library/Application Support/`, which the editor writes to in place. That
directory is not a deployable source: a host cannot pull from a laptop path, and
an rsync someone remembers to run is not a deploy step. The first time it is
forgotten, `/design-kit/` is silently stale while still rendering correctly.

Committing the kit here makes the drift visible instead. `tools/sync-design-kit.sh`
copies the editor's output in; `git status` then shows exactly what a design
session changed, and the host only ever receives what was committed.

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
