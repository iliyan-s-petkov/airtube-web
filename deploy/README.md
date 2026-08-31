# Deployment artefacts

The files in this directory are copied to the host byte-for-byte by the Ansible
role `home.apps.airbg`. `compose_test.go` asserts their security-relevant
properties; read it before changing anything here.

Operator-facing instructions live in [`../docs/deployment.md`](../docs/deployment.md).
This file records the traps — the things that fail silently and would otherwise
be rediscovered the hard way.

## ofelia.ini: three forbidden characters

Command values are parsed twice, by ofelia's INI parser and then by `sh`, and
the two disagree. Each of these is enforced by a test:

| Character | What happens |
|---|---|
| `"` | Stripped anywhere it appears, so `sh` receives a different command than the file reads. `[ -z "$(find ...)" ]` arrives as `[ -z ]`, always true. |
| `;` and `#` | Start an INI comment. The rest of the value is discarded. Observed on the host: the prune job registered as `sh -c 'set -f` and ran nightly doing nothing. |
| backslash | ofelia rejects the entire config (`unquoted ... must be followed by new line or double quote`), crash-loops, and runs no job at all. |

Single quotes are safe. Protect a glob with `set -f`, not with quotes or an
escape — an unprotected `airbg-*.dump` is expanded by `sh` against the
container's working directory before `find` sees it, and one stray file of that
shape turns the search into a literal name that matches nothing.

Use `&&`, `||` and subshells where you would reach for `if ... then ... fi`.

After editing any command, read ofelia's startup log: it prints what it
actually parsed, which is the only place a truncation shows.

## ofelia.ini: what a job inherits

Nothing. Not the daemon's environment, not ofelia's, not the app service's
`env_file`. Every value a job needs is set in its own section. Secrets are
named by a `*_FILE` environment variable and bind-mounted read-only; the
distroless image has no shell to pipe a file into a variable, and writing the
DSN into this file would put a credential in configuration management.

ofelia attaches every job-run container to Docker's default bridge in addition
to the network named by `network =`. A job on the internal `airbg_back` network
therefore still has internet egress, which is how `collect` reaches
data.sensor.community while `db` stays isolated. This is empirical, not
documented behaviour — verified against the pinned image. Re-check it after any
version bump; if it ever changes, `collect` fails at the upstream fetch.

`network =` holds exactly one network per job. A second line replaces the first
rather than adding to it, so "one job on two networks" is not available.

Nothing watches a job-run's exit code. Every failure mode on this page is
silent.

## socket-proxy: why IMAGES=1

ofelia checks that a job's image exists (`GET /images/json`) before creating its
container, and does so even with `pull = false` on every job. With `IMAGES=0`
the proxy answered 403 and no scheduled job ever ran — no collection, no
backup, no error anyone would see.

`IMAGES=1` also re-permits `POST /images/create`, so a compromised ofelia could
fetch an image and run it. That was accepted deliberately, weighed against what
the proxy already grants: `CONTAINERS=1` with `POST=1` is container creation
with arbitrary binds and command, which is already host root. The alternative
that removes the surface instead of narrowing it is host systemd timers in
place of ofelia, which would delete both this service and the scheduler.

`pull = false` stays regardless: `airbg:latest` is built on the host and exists
in no registry, so a pull could only ever fail.

## socket-proxy: why it is not read_only

It is the one service here that is not, and it cannot be. Its entrypoint
renders `haproxy.cfg` from a template beside it on every start:

- `read_only: true` crash-loops it outright.
- A `tmpfs` over `/usr/local/etc/haproxy` crash-loops it more confusingly, by
  hiding the image's own template — the log reads
  `haproxy.cfg.template: No such file or directory`, which looks nothing like a
  permissions problem.

Both were tried against the running host. What actually contains this service
is unchanged and asserted by tests: the socket is mounted `:ro`, every
capability is dropped, no new privileges, and the endpoint allowlist stays
narrow.

## docker-compose.prod.yml: the app's pinned addresses

`app` pins `ipv4_address` on both networks so the SSH forward in
`../docs/deployment.md` has a stable target. The consequence is that
`docker compose run app <cmd>` cannot be used once the stack is up: `run`
builds its one-off container from the same service definition, static addresses
included, and the daemon answers `Address already in use`. It fails only on
re-deploy, never on a first deploy.

The Ansible role runs bootstrap commands and area imports with plain
`docker run` on the back network instead, the same shape ofelia's job-run
containers use.

## Caddyfile: the origin certificate

`origin.pem` / `origin.key` are a **Let's Encrypt** certificate, not a Cloudflare Origin CA
one — the design documents in `../docs/superpowers/` describe the Origin CA route and were
not followed. Reaffirmed 2026-08-31.

`certbot` on the host issues it over DNS-01 against Cloudflare and installs it through a
deploy hook; the Ansible role `home.apps.airbg` (`tasks/certificate.yml`) sets that up and
`certbot.timer` renews it. Caddy itself never runs ACME here — DNS-01 needs neither a public
A record nor port 80, which is what lets the origin stay unreachable except through
Cloudflare.

One certificate covers all three names (`airbg.org`, `www.airbg.org`, `tiles.airbg.org` as
SANs), and it is loaded once, by the `tls` directive in the `airbg.org` block. The
`tiles.airbg.org` block has no `tls` directive of its own: Caddy indexes loaded certificates
by SAN and matches this one. **Dropping a name from the certbot lineage therefore breaks the
tiles vhost, with nothing in this file mentioning it.**

Two consequences of using a publicly trusted certificate rather than an Origin CA one: it
expires in 90 days rather than 15 years, so a stalled `certbot.timer` is an outage; and it is
valid for anyone, not just Cloudflare, so the client-certificate requirement below is the
*only* thing separating the origin from the internet.

## nftables.conf: never `flush ruleset`

`flush ruleset` is not scoped to this file's table. It destroys Docker's
`ip nat` and `ip filter` tables too, and Docker builds those exactly once, at
daemon start. The result is containers with no masquerade and no DNS — image
builds fail on `lookup proxy.golang.org ... i/o timeout` — while `nft list
ruleset` looks perfectly correct. Recovering needs a `systemctl restart
docker`. Delete only the table this file owns.
