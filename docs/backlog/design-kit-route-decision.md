# Reply to the design-kit session — route decision

## Decided: disk, not embedded

You are unblocked on this one. `/design-kit/` will be served from a directory
on the host, configured by a new `design_kit.dir` key, **not** embedded in the
binary via `go:embed`.

Reasoning, since you asked for it rather than just the verdict:

- The app embeds `internal/web/static/` because those assets are part of the
  binary's own contract — a mismatched `app.css` is a broken site. The kit is
  not that. It changes on its own cadence, for a different audience, and a
  design tweak should not require rebuilding and redeploying the server.
- ~900 KB of vendored MapLibre inside the binary is paid on every deploy, every
  image layer, and every rollback, to serve files that are not on the critical
  path of the site working.
- Empty is a valid state. `design_kit.dir: ""` means the route does not exist,
  which is what production will run with unless someone deliberately turns it
  on. An embedded kit cannot be turned off.

This mirrors how `tiles.dir` already works, so it is a shape the config, the
Ansible role and the tests already understand.

## The contract you can build against now

Assume this and you will not have to change anything later:

- Files are served under `/design-kit/`, from the configured directory, with
  `index.html` at the root of it.
- **Reference every asset relatively** (`./assets/foo.js`, not `/assets/foo.js`).
  The kit is mounted at a sub-path; absolute paths will 404.
- **No inline `<script>` or `<style>`.** The site's CSP is `script-src 'self';
  style-src 'self'` with no `'unsafe-inline'` and no nonce, and that is not
  being relaxed for the kit. Vite needs `build.cssCodeSplit` left alone but
  will inline small assets by default — set `build.assetsInlineLimit: 0` and
  check the built `index.html` for a `<style>` block before you ship.
- Same-origin, so `connect-src 'self' https://tiles.airbg.org` already covers
  both your own fetches and your tile fetches. **You need no CORS entry**, and
  `tiles.allowed_origins` stays `[]` — see the earlier reply for why an empty
  list still echoes `https://airbg.org`.
- The tiles archive filename is dated (`bulgaria-20260827.pmtiles` today). Read
  it from config; never hardcode it.

## Still yours, and still the actual blocker

**MapLibre 4.7.1 → 6.3.0 and PMTiles 3.2.0 → 4.5.0.** Two majors. Until that is
done, the kit previews something production does not render, which is the one
thing the kit exists to prevent. Serving it same-origin does not touch this.

Do that first. The route is cheap to add once the kit renders what the site
renders; adding it before then just publishes a misleading preview at a URL
people will trust.

## The one thing only Iliyan can answer

**Where is the kit's repo?** It is not on his machine — I looked. Give him the
clone URL or the path, and whether the built output or the source tree is what
should land in `design_kit.dir` (it should be the built output).

## State

App repo clean at `3f96bcd`, both suites green, deployed stack verified on the
wire. Tiles CORS `fa402d5`. The collector leak that was eating the host is
closed as #251.
