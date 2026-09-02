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

## Correction: the kit is not a repository, and the root is the served root

The earlier version of this file raised a `.git` exposure objection and
recommended pointing `design_kit.dir` at `ui_kits/` rather than a repo root.
Both halves were wrong, and the second would have broken the kit.

The kit lives at:

```
/Users/iliyan/Library/Application Support/Open Design/namespaces/release-stable/data/projects/2bd1e8df-2fb5-4be2-b61c-52d0eb1e3030
```

Note the spaces — quote it in Ansible and rsync. `git rev-parse --show-toplevel`
reports it is not a git repository. It is an OpenDesign project directory,
managed by the editor.

- **The `.git` exposure does not exist in this tree.** There is no `.git`. The
  objection was raised against an assumption, and the kit session corrected it.
- **`ui_kits/` cannot be the served root.** `ui_kits/app/index.html` references
  `../../tokens.css`, `../../colors_and_type.css`, `../../components.css` and
  `../../assets/`. From `ui_kits/app/` that is the PROJECT ROOT, not `ui_kits/`.
  Serving `ui_kits/` gives a page with every stylesheet missing — an unstyled
  render, not an error, which is the failure mode nobody investigates quickly.

So `design_kit.dir` is the project root. What that exposes is not a repository
but an editor's working directory: `CLAUDE.md`, `DESIGN.md`, `SKILL.md`,
`README.md`, `image*.png`, a working screenshot, `examples/`, `preview/`,
`context/`, `node-compile-cache/`, and `.file-versions/` — the editor's
per-file revision history.

**Resolved with an allowlist on the first path segment**, the same pattern
`internal/tiles` uses: `ui_kits`, `assets`, `tokens.css`, `colors_and_type.css`,
`components.css`. An allowlist rather than a denylist because the tree is
written by a tool that creates files nobody decided to create — the set that
must not be served grows on its own; the set that must is five entries.

The dotted-segment refusal is kept alongside it, not replaced by it: the
allowlist bounds the first segment, the dotted refusal bounds every segment
below, where `ui_kits/.file-versions/` also lives.

## Contract

- Files are served under `/design-kit/` from `design_kit.dir`.
- **The entry is `ui_kits/app/index.html`**, not an `index.html` at the served
  root. `/design-kit/` redirects to it at the route layer. Agreed the kit should
  not add a root-level launcher: one page, one owner.
- **Only five roots are reachable**: `ui_kits/`, `assets/`, `tokens.css`,
  `colors_and_type.css`, `components.css`. All five verified present. Adding a
  sixth means editing `allowedRoots` in `internal/designkit`.
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

## Built

`5caebf2`, hardened in the follow-up commit. `internal/designkit` serves
`design_kit.dir` under `/design-kit/`, registered on the public mux inside the
middleware chain. `design_kit.dir` is `""` in the committed `airbg.yaml`, so
the route does not exist in production.

Mutation-verified: **11 mutations, 11 caught.** The ones worth remembering:

- **The root allowlist, and its degradation into a dotfile denylist.** Both
  mutations are caught only because the fixture is shaped like the real project
  directory — `CLAUDE.md`, scratch dirs and `.file-versions/` included. A
  fixture holding only the kit cannot fail either.
- **The dotfile guard checks every segment, not the first.** This survived the
  first round: every dotted case in the original fixture sat at the served root,
  so a first-segment check passed all of them while still serving nested ones.
- **The entry redirect pointing one level too shallow** — the exact mis-set the
  `../../` nesting punishes, and it renders as an unstyled page rather than an
  error.
- **The entry redirect made absolute.** `http.Redirect` resolves a relative
  target against the request path; under `StripPrefix` that path is `/`, so it
  emitted `/ui_kits/app/`, outside the route. `Location` is set directly.
- **Directories are never listed.** `http.ServeFileFS` produces a listing when
  told not to redirect — a real default being overridden.
- **Route inside the chain.** Registering it outside still serves the kit, with
  no CSP and no rate limit and no other symptom. Pinned by a server-level test
  asserting the response carries the configured CSP.

Two of the eleven survived their first round, and both share a shape: the
mutation leaves the feature working and changes only an invisible property.

`airbg.yaml` ships inside the image (`roles/airbg/tasks/artefacts.yml:58`), so
the new required key needs no Ansible change and cannot drift on the host.

## The kit is now a git repository

Decided and done. `git init` in place in the OpenDesign project directory, one
import commit, 66 files. In place rather than a copy, because the editor keeps
writing there and a repo it does not write to is a repo that drifts.

`.gitignore` excludes what the editor generates rather than what anyone wrote —
`.file-versions/`, `node-compile-cache/`, `context/`, `preview/`,
`*.artifact.json`, working screenshots and `image*.png`. None of it is served by
the allowlist, and none of it is a decision anyone made. `CLAUDE.md` is excluded
deliberately, matching this repo's rule.

`README-repo.md` in the kit records why the directory is versioned, that
`design_kit.dir` must be the project root and not `ui_kits/`, and that adding a
sixth served root means editing `allowedRoots` rather than adding a file.

Note: the import commit is gitsign-signed under the `dojobits.io` identity from
the global git config. This is not a DojoBits project. Worth resetting before a
remote is added, if that identity matters on the eventual host.

## Still needed

1. **A remote.** The repo is local-only. Creating it is not mine to do — no
   credentials, and where the kit should live is the same kind of question as
   whether it should be versioned at all.
2. **Ansible wiring.** Once a remote exists, the role clones or fetches it to a
   path on the host and sets `design_kit.dir` to that path. Until then the
   shipped `design_kit.dir: ""` keeps the route non-existent, which is the
   correct state.

## State

App repo clean; both suites green; stack deployed and verified on the wire.
Rate limiting and the enumeration guard mutation-verified separately: 17
mutations, 16 caught, the survivor fixed in `2016a68`.
