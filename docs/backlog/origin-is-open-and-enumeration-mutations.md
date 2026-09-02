# The origin accepts any client, and what the enumeration test actually proves

Two cutover blockers were checked on 2026-09-02. One passed. One found a live
security gap.

## The origin is open, with the closed configuration installed

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

Config on disk ≠ config in memory: Caddy is still running an earlier
configuration, and the only one that behaves this way is `Caddyfile.dev`, which
exists precisely to drop `client_auth`. The reload handler is notified by the
copy task, so a run that does not change the file does not reload — and the last
two runs reported `changed=0` for it.

This is why `airbg.org` was browsable from the LAN at all. Every wire check of
the design kit went through the open origin, which does not invalidate those
results — the app served them — but it does mean nothing has ever exercised the
closed path.

**Not yet fixed.** Confirming the running config and forcing a reload needs
`docker compose exec caddy caddy reload` on the host, which this session's
permissions do not currently allow.

The blocker on #64 was written as "re-deploy without `airbg_open_origin` and
confirm alert 116". That was already the deployed state, and it still serves
everyone, so the check is not a formality.

`playbooks/verify.yml` has no origin-pull assertion. It checks published ports,
the nftables policy, credential modes and the backup marker — none of which can
see this. A verify task that fails when the origin completes a handshake without
a client certificate would have caught it.

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
