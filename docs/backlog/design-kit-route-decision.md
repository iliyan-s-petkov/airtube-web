# Design-kit route — decision and contract

Supersedes the first version of this file, which assumed the kit was a Vite
build. It is not: no bundler, no `package.json`, the source tree is the
deployable output. Everything below is rewritten around that.

## Decided: disk, not embedded

`/design-kit/` is served from a directory on the host, configured by a new
`design_kit.dir` key, **not** embedded in the binary via `go:embed`.

- The app embeds `internal/web/static/` because those assets are part of the
  binary's contract — a mismatched `app.css` is a broken site. The kit is not
  that: it changes on its own cadence, for a different audience.
- Roughly 1 MB of vendored MapLibre in the binary is paid on every deploy,
  image layer and rollback, for something off the critical path.
- `design_kit.dir: ""` means the route does not exist. That is what production
  runs unless someone deliberately turns it on. An embedded kit cannot be
  switched off.

Same shape as `tiles.dir`, which the config, the Ansible role and the tests
already understand.

## Blocking objection: do not point `design_kit.dir` at a repo root

The kit session asks for `design_kit.dir` to be the **kit repo root**, because
there is no `dist/`. As specified that serves `.git/` over HTTPS.

A static file server rooted at a working tree exposes `.git/config`,
`.git/HEAD`, and every object in `.git/objects/` — which is the full history,
not just the current tree. Anything ever committed and later removed is
retrievable. That is a standard, actively scanned-for exposure, and it is worse
here than the usual case because this route would be reachable from the public
site rather than an internal tool.

It is not hypothetical for a no-build kit specifically: with a build step the
served directory is generated output that never contains a `.git`. Dropping the
build is what creates the exposure.

Two ways to resolve it, both fine by me. **Kit session picks, since it owns the
layout:**

1. **Serve `ui_kits/`, not the repo root.** `design_kit.dir` points one level
   in. The `../../tokens.css` references still resolve, because `ui_kits/app/`
   is still two levels below the served root — the nesting the kit says is
   load-bearing is preserved exactly. This is my recommendation: it needs no
   code in the handler and no denylist to get right.
2. **Keep the repo root and have the handler refuse dotted path segments.**
   Workable, but it is a denylist, and a denylist is the thing that is wrong
   the first time someone adds `.env.example` or a `.well-known` directory.

Whichever is chosen, the handler will refuse dotfile segments anyway — defence
in depth, not a substitute for pointing at the right directory.

## Contract

- Files are served under `/design-kit/` from `design_kit.dir`.
- **The entry is `app/index.html`** (relative to the served root under option 1
  above), not an `index.html` at the served root. `/design-kit/` will redirect
  to it at the route layer. Agreed the kit should not add a root-level
  launcher: one page, one owner.
- **Relative references only.** Confirmed already satisfied.
- **No inline `<script>` or `<style>`.** The site's CSP is `script-src 'self';
  style-src 'self'` with no `'unsafe-inline'` and no nonce, and it is not being
  relaxed for the kit. Accepted that with no build step nothing can reintroduce
  them; the constraint is recorded because it binds any future build step too.
- Same-origin, so `connect-src 'self' https://tiles.airbg.org` already covers
  the kit's own fetches and its tile fetches. **No CORS entry is needed** and
  `tiles.allowed_origins` stays `[]`.
- Archive filename read from `style.json`. Confirmed correct.

## Their findings, checked against this repo

- **`maplibregl.supported()` removed in 6.x** — does not affect us.
  `web/src/islands/map.js:6` imports the named exports `Map` and `addProtocol`
  rather than the default namespace, and never calls `supported()`.
- **The three-file import graph** (`maplibre-gl.mjs` → `maplibre-gl-shared.mjs`,
  plus `maplibre-gl-worker.mjs`) — already handled. `web/vite.config.js:5-21`
  documents both 404s in the order they were found: the worker first, then the
  shared chunk, which only surfaced once the worker started running. Neither is
  content-addressed, so both are pinned to unhashed names.
- **`worker-src 'self' blob:`, both halves load-bearing** — useful. 6.3.0
  constructs workers as `new Worker(url, {type:'module'})` *and* via
  `createObjectURL`. Recorded, because the obvious "tidy-up" of dropping
  `blob:` would break the map at runtime with no build-time signal.

## Still needed from Iliyan

The kit's repo path or clone URL — it is not on this machine.

## State

App repo clean at `2016a68`; both suites green; stack deployed and verified on
the wire. Rate limiting and the enumeration guard mutation-verified: 17
mutations, 16 caught, the survivor fixed in `2016a68`.
