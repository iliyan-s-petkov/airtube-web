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

**`network =` does not work here, and fails silently.** Attaching a container to
a network is a `/networks` API call, and the socket proxy sets `NETWORKS=0`, so
the proxy answers 403 and ofelia creates the container anyway. The job lands on
Docker's default bridge alone, where no compose service name resolves. An
earlier note in this file claimed the opposite from observing that job
containers reach the default bridge — they do, but that is the *only* network
they get, not an extra one.

The nightly `pg_dump` failed this way on every run, with
`could not translate host name "db" to address` visible only in ofelia's own
log. It is a host systemd timer now; see below. No job left here needs a
network — `backup-prune` reads a volume and nothing else.

Nothing watches a job-run's exit code. Every failure mode on this page is
silent.

## Why the backup is a systemd timer

`airbg-backup.timer` and `airbg-backup.service` run the nightly dump from the
host, as a plain `docker run --network airbg_back` — the same shape the role's
bootstrap tasks already use, and the one that demonstrably resolves `db`.

This is the alternative named below under `IMAGES=1`: removing the surface
instead of widening it. Making the ofelia job work would have meant
`NETWORKS=1` on the proxy, which with `POST=1` permits creating, connecting and
deleting networks — enough to attach a container to the edge network. A timer
needs nothing from the proxy at all.

Two things improve in passing. `Persistent=true` runs a dump missed while the
host was down, which ofelia could not do. And the unit's exit status is visible
to `systemctl` and the journal, where a failed job-run was visible only in
ofelia's log. The `BACKUP-IS-STALE` marker remains the alarm that matters — it
is what caught this — and `backup-prune` still raises it at 03:45.

The container runs `--user 0:0`. The image's default uid 1000 can read neither
`/srv/airbg/pgpass` — which `pg_dump` requires to be `0600`, and which the role
writes as root — nor `/var/backups/airbg`. `docker run` honours the image's
`USER`; ofelia does not, because `RunJob.User` carries `default:"root"`. That is
why `backup-prune` still deletes root-owned dumps and rewrites the marker with
no `user =` of its own, and why moving the same command to a unit needed the
flag. Without it: `could not open output file "/backups/.partial.dump":
Permission denied`.

In the unit, `%` is a systemd specifier, so `date`'s format is written `%%Y%%m%%d`
to reach `sh` as `%Y%m%d`. Written singly, systemd expands it and the dump is
named after whatever the specifier meant.

## socket-proxy: why IMAGES=1

ofelia checks that a job's image exists (`GET /images/json`) before creating its
container, and does so even with `pull = false` on every job. With `IMAGES=0`
the proxy answered 403 and no scheduled job ever ran — no backup, and no error
anyone would see.

`IMAGES=1` also re-permits `POST /images/create`, so a compromised ofelia could
fetch an image and run it. That was accepted deliberately, weighed against what
the proxy already grants: `CONTAINERS=1` with `POST=1` is container creation
with arbitrary binds and command, which is already host root. The alternative
that removes the surface instead of narrowing it is host systemd timers in
place of ofelia, which would delete both this service and the scheduler.

`pull = false` stays regardless: `airbg:latest` is built on the host and exists
in no registry, so a pull could only ever fail.

## Why there is no collect job

`airbg serve` polls upstream in-process, because the snapshot the server
returns lives in that process's memory and no separate container can swap the
pointer to it — `cmd/airbg/main.go:261` has the reasoning. Scheduling
`airbg collect` alongside it is therefore redundant, and actively harmful:
that command runs its own poll loop and never returns, so ofelia never reaps
its container. `delete = true` does not help, because the deletion happens when
the job *finishes*.

Left running, it leaked one immortal container every five minutes. By the time
it was found the host held 531 of them, sat at load 750 with 116 MB free and no
swap, and answered ssh too slowly for Ansible's 10-second timeout — so the
deploy that would have fixed it could not run either. `TestNoOfeliaJobRunsTheCollector`
pins this by command rather than by job name, since the same leak reappears
under any name.

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

## docker-compose.prod.yml: single-file bind mounts

`./Caddyfile` and `./ofelia.ini` are mounted one file at a time, and a file bind mount
follows the **inode**, not the path. Any writer that replaces the file — which is what an
atomic write, `mv`, and Ansible's `copy` all do — leaves the container reading the old
inode forever. Restarting the container does not help; only recreating it does.

The failure is silent in the worst way: the host file reads correctly, `caddy reload`
re-parses the stale content and reports success, and ofelia restarts happily on config it
already had. The role therefore writes both files with `unsafe_writes: true`, which writes
through the existing inode. That trades atomicity for the mount actually working; a
truncated write fails Caddy's config validation and the running config survives.

If you edit either file on the host by hand, edit in place (`sed -i` without a suffix is
not in place on all platforms — check). Do not `mv` a new file over it.

## Caddyfile.dev: the dev Caddyfile

`Caddyfile.dev` is `Caddyfile` without the `client_auth` block. It exists for one
situation: the host has no public IP and no port forward, and the site has to be browsed
from the LAN before anything is exposed. The role installs it only when run with
`-e airbg_open_origin=true`, prints a warning task when it does, and installs the
production file otherwise.

It must be the real hostnames. `AIRBG_LISTEN_BASE_URL` is `https://airbg.org` and the CSP
allows `connect-src 'self' https://tiles.airbg.org`, so browsing the VM by IP or by any
other name loads a page whose tile requests the browser then blocks — an empty map that
looks like a tiles bug. LAN DNS answers both names locally; see the OPNsense repo's
README, "airbg.org on the LAN".

What is genuinely lost while this file is installed: the origin accepts any client that
can reach port 443. Rate limiting, the tiering, and every application-level control still
apply, but the certificate wall does not — so this is safe only while no route from the
internet exists. Before cutover, re-deploy without the flag and confirm a direct
`openssl s_client` to the origin fails the handshake.

`compose_test.go` asserts the production file still requires `require_and_verify`, and
separately that this file carries its banner and does *not* require a client certificate.
Adding `client_auth` back here does not make the deployment safer; it removes the dev path
and fails the tests.

## nftables.conf: never `flush ruleset`

`flush ruleset` is not scoped to this file's table. It destroys Docker's
`ip nat` and `ip filter` tables too, and Docker builds those exactly once, at
daemon start. The result is containers with no masquerade and no DNS — image
builds fail on `lookup proxy.golang.org ... i/o timeout` — while `nft list
ruleset` looks perfectly correct. Recovering needs a `systemctl restart
docker`. Delete only the table this file owns.
