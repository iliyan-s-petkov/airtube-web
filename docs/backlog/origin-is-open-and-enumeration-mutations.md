# A bind mount pinned the origin open, and what the enumeration test proves

Two cutover blockers were checked on 2026-09-02. One passed. One found a live
security gap, closed on 2026-09-03.

## The origin was open, with the closed configuration installed

`deploy/Caddyfile` — the one `airbg_open_origin: false` selects, and the default
— puts `client_auth { mode require_and_verify }` on the `airbg.org` vhost. That
file is on the host: `grep client_auth /srv/airbg/Caddyfile` matches at line 19.

The origin does not enforce it. From a laptop on the LAN, with no client
certificate at all:

    printf 'GET / HTTP/1.1\r\nHost: airbg.org\r\nConnection: close\r\n\r\n' \
      | openssl s_client -connect 192.168.1.176:443 -servername airbg.org -quiet
    HTTP/1.1 200 OK

No TLS alert 116, no handshake failure, a served page. `tiles.airbg.org` answers
404 on `/`, so both vhosts are live and this is not a catch-all swallowing the
request.

### The cause: a single-file bind mount pins an inode, not a path

The first guess — "the reload handler never fired" — was wrong, and forcing a
reload changed nothing. `caddy reload` returned rc=0 and logged `config is
unchanged`, which was true, and the container's own view said why:

    host  /srv/airbg/Caddyfile          inode 1630114  production, client_auth
    guest /etc/caddy/Caddyfile          inode 1620297  Caddyfile.dev

Compose bind-mounts that Caddyfile one file at a time, so the mount is a pin to
an inode. Ansible's `copy` writes a temporary file and renames it over the
target, which is the right way to replace a config and the wrong way to feed
one to a container: the rename produces a new inode and the mount keeps
resolving to the old, now-unlinked one. Caddy went on reading the file the
guest was installed with on 2026-08-31 — `Caddyfile.dev`, whose whole purpose
is to drop `client_auth`. The `www.airbg.org` connection policy in the running
config, a name that appears in no other file, confirmed which one it was.

Every layer reported success honestly. The copy was idempotent, `changed=0` was
accurate, the reload succeeded, and `config is unchanged` was literally correct
about a config nobody had installed. Only a request on the wire disagreed.

This is why `airbg.org` was browsable from the LAN at all. Every wire check of
the design kit went through the open origin, which does not invalidate those
results — the app served them — but nothing had ever exercised the closed path.

### The fix

`unsafe_writes: true` on the Caddyfile copy (already present, added after the
detaching write) keeps a future replace in place, preserving the inode. It
cannot reattach a mount already detached, and a reload cannot reach a file the
container can no longer see — so the role now compares `sha256sum
/etc/caddy/Caddyfile` inside the container against the host file and recreates
only the caddy service when they differ. Handlers are flushed first, so the
comparison judges a caddy that has already applied whatever this run changed.

Two tasks after it ask the origin for a page while presenting no certificate and
fail the play if it answers. Verified after the deploy: `airbg.org` sends
`Acceptable client certificate CA names` and serves nothing without one, while
`tiles.airbg.org` still answers 404 on `/healthz` with no certificate, as a
grey-cloud host must. A second run is `changed=0` with the assertion still
passing.

`https://airbg.org/design-kit/` is no longer reachable from the LAN. That is the
intended consequence, not a regression.

The blocker on #64 was written as "re-deploy without `airbg_open_origin` and
confirm alert 116". That was already the deployed state and it still served
everyone, so the check was not a formality.

`playbooks/verify.yml` still has no origin-pull assertion. It checks published
ports, the nftables policy, credential modes and the backup marker — none of
which can see this. The deploy now asserts it, but verify runs on its own and
should carry the same check.

## The enumeration mutation check: the test holds

`TestEndToEndEnumerationTrips` (`internal/server/e2e_test.go:234`, build tag
`integration`) was checked by mutation rather than by reading it. Baseline
passes.

| Mutation | e2e | `internal/ratelimit` | `internal/api` |
|---|---|---|---|
| Wall never trips (`if false && len(set) >= limit`) | **FAIL** | FAIL | FAIL |
| Off-by-one (`>=` → `>`) | pass | **FAIL** (7, incl. 2 boundary tests) | FAIL |
| Wall intact but its answer ignored in the handler | **FAIL** | pass | **FAIL** |

The e2e test survives the off-by-one because it walks `limit + 2` areas, so a
wall that trips one request late still trips inside the walk. That is not a gap:
the boundary is pinned by `TestAreaLimitBoundaryIsExclusive` and its sensor
twin, and asking the slow integration test to also pin the exact threshold would
duplicate them.

What the e2e test uniquely catches is the third row — the limiter is correct and
correctly configured, and the handler does not act on it. Every unit test in
`internal/ratelimit` passes with the origin wide open to enumeration. That is
the case a stubbed test cannot see, and it is the reason this test exists.
