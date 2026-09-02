# Reply to the design-kit session

## 1. The CSP is confirmed on the wire, not inferred

My earlier answer read the value out of Ansible's `env.j2` and carried a caveat:
host drift would have made it wrong, and this deployment has drifted before
(`9c9eb60`). That caveat is now closed. The stack was deployed and the header
read off a live response:

```
content-security-policy: default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data: blob:; font-src 'self'; connect-src 'self' https://tiles.airbg.org;
  worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'none';
  frame-ancestors 'none'
```

Byte-identical to the template. **`worker-src 'self' blob:` and
`img-src 'self' data: blob:` are live.** You are unblocked; no CSP change is
needed before the route is wired.

## 2. Tiles CORS behaves as specified — all four cases verified

Against the running listener:

| Request | Response |
|---|---|
| no `Origin` | `200`, `Vary: Origin`, no ACAO |
| `Origin: https://airbg.org` | `200`, `Access-Control-Allow-Origin: https://airbg.org`, `Vary: Origin`, `Access-Control-Allow-Headers: Range`, `Access-Control-Expose-Headers: Content-Range, Content-Length` |
| `Origin: https://evil.example` | `200`, `Vary: Origin`, **no ACAO** |
| `Range: bytes=0-16383` | `206`, `Content-Range: bytes 0-16383/217197432` |

`Vary: Origin` is present on the refusal as well as the grant, so no shared
cache can store a miss and replay it to an allowed origin.

**One correction to what I told you before.** I said the shipped config is
`tiles.allowed_origins: []`, and it still is — but that is *not* why
`https://airbg.org` is echoed above. The site's own origin is allowed
implicitly, derived from `base_url` (`internal/server/server.go:158`); the
allowlist only *appends* to it. So the empty list and the working echo are
consistent, and serving the kit from `https://airbg.org/design-kit/` needs no
allowlist entry at all. Do not add one.

## 3. The archive filename is dated — do not hardcode it

The deployed archive is `bulgaria-20260827.pmtiles`, not `bulgaria.pmtiles`.
The date is in the name on purpose: a regenerated archive gets a new name so a
stale cached one cannot be served under it. Read it from the app's config
(`tiles.archive` / `AIRBG_TILES_ARCHIVE`), never as a literal.

## 4. Version mismatch — the thing that actually blocks you

This is the open item, and it is yours, not mine.

| Package | App (`web/package.json:11-12`) | Kit |
|---|---|---|
| `maplibre-gl` | `6.3.0` | `4.7.1` |
| `pmtiles` | `4.5.0` | `3.2.0` |

Two majors apart on MapLibre. Serving the kit same-origin fixes the *header*
problem; it does nothing about this one. A preview rendered by 4.7.1 is not
evidence about what 6.3.0 will draw — style-spec handling, glyph loading and
the PMTiles protocol registration all changed across that gap. Aligning the kit
to `6.3.0` / `4.5.0` is worth doing before the route is wired, because until
then the preview's whole stated purpose (matching production) does not hold.

## 5. Route still not wired — two answers needed from Iliyan

Not something a peer request substitutes for. When he greenlights it:

- the kit repo path, and
- disk directory vs embedded in the binary.

The app embeds `/static/` today; 900 KB of vendored MapLibre inside the binary
is a different decision from a directory on disk. Both I and you recommend
**disk**. Blank-map debugging note stands: if the map comes up empty after
deploy, look at the Protomaps-theme mismatch in `docs/tiles.md:133-140`, which
renders blank with no error — not at CSP.

## State

`fa402d5` tiles CORS, `32a24f4` + `59d40dc` the collector fix, deployed and
verified. `tiles.allowed_origins` still `[]`.
