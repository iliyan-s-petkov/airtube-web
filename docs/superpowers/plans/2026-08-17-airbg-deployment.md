# airbg.org Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce the committed artefacts and tests that let airbg.org run on one VPS behind Cloudflare, with the origin serving only requests that carry Cloudflare's client certificate.

**Architecture:** A `deploy/` directory holds the production Compose file, Caddyfile, ofelia schedule, nftables ruleset and `.env.example`. A test-only Go package `deploy` asserts the security-relevant facts of those files so they cannot rot silently. Caddy is the only service with published ports; the app publishes none in any file, in any environment.

**Tech Stack:** Docker Compose, Caddy 2 (TLS + mTLS), ofelia + docker-socket-proxy (scheduling), TimescaleDB/PostGIS image, nftables, Go 1.26 with `gopkg.in/yaml.v3` (already a dependency).

**Spec:** `docs/superpowers/specs/2026-08-17-airbg-deployment-design.md`. Read it once before Task 1; it explains *why* each assertion exists.

## Global Constraints

- **No new Go dependency, ever.** `gopkg.in/yaml.v3 v3.0.1` is already in `go.mod` and is the only parser available. Do not add a Caddyfile parser, a dotenv parser, or a compose library.
- **No new frontend dependency.** The `web/` tree is not touched by this plan at all.
- **`www-root/` (legacy PHP) is never modified.**
- **Module path is exactly `airbg.org`.** Test package import paths follow from it.
- **No defaults compiled into the binary.** A missing config key is a startup error; `AIRBG_CONFIG` is mandatory. Nothing in this plan adds a fallback.
- **Secrets never enter the repo.** `deploy/.env.example` carries variable names and explanations, never values. `AIRBG_DATABASE_URL` is env-only.
- **`CLAUDE.md` is never staged or committed.** It is gitignored on purpose.
- **No Claude attribution.** No `Co-Authored-By` trailer, no "Generated with" line, in any commit message or file.
- **Every new test must be mutation-proven.** Break the thing the test protects, watch the test fail, restore. Restore with `cp` from a backup — never `git checkout`, which has destroyed unstaged work on this project before.
- **The four test tiers must stay green:** `go test ./...`, `go test -tags integration ./...`, `cd web && npm test`, `go test -tags e2e ./internal/e2e/`. Also `go vet ./...` and `go vet -tags integration ./...`.
- **`gofmt` clean.** Run `gofmt -l .` and expect no output.
- **Compose files in `deploy/` use map-form `networks:` and list-form `environment:`** so the test structs stay simple and unambiguous. This is a rule the test relies on.
- **`git log` needs `--no-show-signature`** on this repo (gitsign/Sigstore) or it hangs.

---

### Task 1: Retune the enumeration rate limit

The shipped limit is 12 distinct areas per hour. Bulgaria has 28 oblasti and comparing them is the site's obvious use, so a curious visitor's 13th area page currently returns `Retry-After: 900`. Ship 30.

**Files:**
- Modify: `airbg.yaml` (the `ratelimit.enumerate` block, and the `listen.addr` comment)
- Modify: `internal/config/resolve_test.go:28`
- Modify: `internal/config/inert_test.go:105`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: nothing later tasks depend on. This task is independent and goes first because it is the only Go-behaviour change in the plan.

- [ ] **Step 1: Run the two tests that pin the old value, and watch them pass**

```bash
go test ./internal/config/ -run 'TestShippedValuesMatchPhase2Behaviour|TestResolve' -v 2>&1 | tail -20
```

Expected: PASS. This is the "before" state — you are about to make them fail on purpose.

- [ ] **Step 2: Change the shipped value**

In `airbg.yaml`, inside the `ratelimit.enumerate:` block, change `areas_per_window: 12` to `areas_per_window: 30`. Replace the comment above it with one that records the decision:

```yaml
  enumerate:
    # Bulgaria has 28 oblasti and comparing them is the site's obvious use, so
    # this covers the whole set in one sitting plus a couple of repeats. It was
    # 12, which turned an ordinary browsing session into a 15-minute wall at the
    # 13th area page. Areas are a bounded set: 30/hour still hard-stops
    # sustained extraction, which is what breadth limiting is for.
    areas_per_window: 30
    sensors_per_window: 40
    window: "1h"
    retry_after: "900s"
```

Leave `sensors_per_window`, `window` and `retry_after` exactly as they are.

- [ ] **Step 3: Run the tests and watch them fail**

```bash
go test ./internal/config/ -run 'TestShippedValuesMatchPhase2Behaviour|TestResolve' 2>&1 | tail -20
```

Expected: FAIL, twice — `Enumerate.AreasPerWindow = 30, want 12` and a `ratelimit.enumerate.areas_per_window` row mismatch. Two failures, not one; if you see only one, you have not found both pins.

- [ ] **Step 4: Update the plain pin**

In `internal/config/resolve_test.go` around line 28, change the expected value:

```go
	if got, want := cfg.RateLimit.Enumerate.AreasPerWindow, 30; got != want {
		t.Errorf("Enumerate.AreasPerWindow = %d, want %d", got, want) // raised from 12, see docs/superpowers/specs/2026-08-17-airbg-deployment-design.md
	}
```

- [ ] **Step 5: Update the inert pin, with the reason**

`internal/config/inert_test.go`'s `TestShippedValuesMatchPhase2Behaviour` exists to prove the Phase 3b config sweep changed **no** behaviour. This is the first deliberate divergence, so the row does not merely get a new number — it gets a comment, or the test quietly degrades into "whatever the config currently says".

Around line 105, replace the row:

```go
			// The one deliberate divergence from Phase 2 behaviour: raised from
			// 12 because Bulgaria has 28 oblasti and comparing them is the
			// site's obvious use. See
			// docs/superpowers/specs/2026-08-17-airbg-deployment-design.md.
			// Every other row in this table still means "unchanged since Phase 2".
			{"ratelimit.enumerate.areas_per_window", float64(cfg.RateLimit.Enumerate.AreasPerWindow), 30},
```

- [ ] **Step 6: Run the tests and watch them pass**

```bash
go test ./internal/config/ 2>&1 | tail -5
```

Expected: `ok  	airbg.org/internal/config`

- [ ] **Step 7: Fix the `listen.addr` comment so production does not read as a mistake**

`airbg.yaml`'s comment on `listen.addr` currently ends with "Do not 'fix' a container that answers nothing by binding 0.0.0.0." Production does exactly that — safely, because the container publishes no port. Replace the whole comment block above `addr:` with:

```yaml
listen:
  # Loopback by default and on purpose. The origin must be unreachable except
  # through the CDN: CF-Connecting-IP is the only rate-limit bucket key, so a
  # scraper that reaches the origin directly bypasses every limiter in one hop.
  #
  # The rule is "never expose the origin", not "never bind 0.0.0.0". The
  # production deployment overrides this to 0.0.0.0:8080 and is safe doing so,
  # because that container publishes no port and is reachable only from the
  # reverse proxy on an internal Docker network — the namespace does the
  # enforcing. Binding 0.0.0.0 on a host with a published port is still the
  # mistake this comment warns about. See deploy/ and docs/deployment.md.
  addr: "127.0.0.1:8080"
```

- [ ] **Step 8: Verify the config still validates and nothing else moved**

```bash
go test ./internal/config/ ./cmd/airbg/ 2>&1 | tail -5
git diff --stat
```

Expected: both packages `ok`; the diff touches exactly `airbg.yaml`, `internal/config/resolve_test.go`, `internal/config/inert_test.go`.

- [ ] **Step 9: Mutation-prove the pin still bites**

The point of the pins is that an unexplained retune fails the suite. Prove they still do.

```bash
cp airbg.yaml "$CLAUDE_JOB_DIR/tmp/airbg.yaml.bak"
perl -pi -e 's/areas_per_window: 30/areas_per_window: 45/' airbg.yaml
go test ./internal/config/ -run 'TestShippedValuesMatchPhase2Behaviour|TestResolve' 2>&1 | tail -10
```

Expected: FAIL in both tests. Then restore and confirm green:

```bash
cp "$CLAUDE_JOB_DIR/tmp/airbg.yaml.bak" airbg.yaml
go test ./internal/config/ 2>&1 | tail -3
```

- [ ] **Step 10: Commit**

```bash
git add airbg.yaml internal/config/resolve_test.go internal/config/inert_test.go
git commit -m "config: raise the area enumeration limit to 30 per hour

Bulgaria has 28 oblasti and comparing them is what the site is for, so the
shipped 12 turned an ordinary browsing session into a 15-minute wall at the
13th area page. Areas are a bounded set, so 30/hour still stops sustained
extraction.

This is the first deliberate divergence from Phase 2 behaviour, so the row in
TestShippedValuesMatchPhase2Behaviour carries the reason rather than just a new
number."
```

---

### Task 2: The production Compose file and its first invariants

The security-relevant facts of the deployment are file-level facts, which is the shape that rots silently. The test comes first.

**Files:**
- Create: `deploy/compose_test.go`
- Create: `deploy/docker-compose.prod.yml`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `deploy/compose_test.go` defines `loadCompose(t *testing.T) composeFile` and the types `composeFile`, `composeService`, `composeNetwork`, used by Tasks 3, 4 and 5. Exact definitions are in Step 3 below. `deploy/docker-compose.prod.yml` defines services named `caddy`, `app`, `db`, `ofelia`, `socket-proxy` and networks named `edge`, `back`, `sched`; later tasks add to it without renaming anything.

- [ ] **Step 1: Create the directory**

```bash
mkdir -p deploy
```

- [ ] **Step 2: Confirm a test-only Go package is legal here**

A package with no non-test files is fine for `go test` and `go vet` (verified on Go 1.26), which is why `deploy/` can hold YAML plus one `_test.go` and nothing else. No `doc.go`, no placeholder — adding one would be cargo cult.

- [ ] **Step 3: Write the failing test — loader plus the three port invariants**

Create `deploy/compose_test.go`:

```go
// Package deploy holds no Go code. It holds the production deployment
// artefacts, and this test, which asserts the security-relevant facts about
// them.
//
// Every assertion here protects a property that is invisible at review time: a
// published port, a container on the wrong network, a widened Docker socket.
// Each one is a fact you could break in a plausible edit and not notice until
// something is scraping the API from an address the rate limiter never sees.
package deploy

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// The production Compose file uses map-form `networks:` and list-form
// `environment:` throughout, so these structs are unambiguous. That is a rule
// the file must follow, not a coincidence to preserve by luck.
type composeFile struct {
	Name     string                    `yaml:"name"`
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks"`
}

type composeService struct {
	Image       string                    `yaml:"image"`
	Ports       []string                  `yaml:"ports"`
	Networks    map[string]composeAttach  `yaml:"networks"`
	Environment []string                  `yaml:"environment"`
	Volumes     []string                  `yaml:"volumes"`
}

type composeAttach struct {
	IPv4Address string `yaml:"ipv4_address"`
}

type composeNetwork struct {
	Internal bool `yaml:"internal"`
	IPAM     struct {
		Config []struct {
			Subnet string `yaml:"subnet"`
		} `yaml:"config"`
	} `yaml:"ipam"`
}

func loadCompose(t *testing.T) composeFile {
	t.Helper()
	data, err := os.ReadFile("docker-compose.prod.yml")
	if err != nil {
		t.Fatalf("ReadFile(docker-compose.prod.yml) error = %v, want nil", err)
	}
	var c composeFile
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("Unmarshal(docker-compose.prod.yml) error = %v, want nil", err)
	}
	return c
}

func service(t *testing.T, c composeFile, name string) composeService {
	t.Helper()
	svc, ok := c.Services[name]
	if !ok {
		t.Fatalf("no service %q in docker-compose.prod.yml", name)
	}
	return svc
}

// A published app port is a direct-to-origin entrance, and the entire design
// exists to deny one: CF-Connecting-IP is the rate-limit bucket key and is
// attacker-controlled on a direct connection. This is the single most
// load-bearing line in the deployment.
func TestAppPublishesNoPort(t *testing.T) {
	if got := service(t, loadCompose(t), "app").Ports; len(got) != 0 {
		t.Errorf("app publishes ports %v, want none", got)
	}
}

// The database is reachable from the app and from nothing else. A published
// port would put Postgres on the public internet; membership of the edge
// network would put it one compromised reverse proxy away.
func TestDatabaseIsNotReachable(t *testing.T) {
	db := service(t, loadCompose(t), "db")
	if got := db.Ports; len(got) != 0 {
		t.Errorf("db publishes ports %v, want none", got)
	}
	if _, on := db.Networks["edge"]; on {
		t.Error("db is attached to the edge network, want back only")
	}
}

// Exactly one service faces the internet, and it faces it on exactly two
// ports. Anything else acquiring a ports: key is the regression this catches.
func TestOnlyCaddyPublishesPorts(t *testing.T) {
	c := loadCompose(t)
	for name, svc := range c.Services {
		if name == "caddy" {
			continue
		}
		if len(svc.Ports) != 0 {
			t.Errorf("service %s publishes ports %v, want none — only caddy may", name, svc.Ports)
		}
	}
	want := map[string]bool{"80:80": true, "443:443": true}
	caddy := service(t, c, "caddy")
	if len(caddy.Ports) != len(want) {
		t.Fatalf("caddy publishes %v, want exactly %d ports", caddy.Ports, len(want))
	}
	for _, p := range caddy.Ports {
		if !want[p] {
			t.Errorf("caddy publishes %q, which is not 80:80 or 443:443", p)
		}
	}
}
```

- [ ] **Step 4: Run the test and watch it fail**

```bash
go test ./deploy/ 2>&1 | tail -10
```

Expected: FAIL — `ReadFile(docker-compose.prod.yml) error = open docker-compose.prod.yml: no such file or directory`.

- [ ] **Step 5: Write the Compose file**

Create `deploy/docker-compose.prod.yml`. Note `name: airbg` — it fixes the Compose project name, which makes the created network names deterministically `airbg_edge`, `airbg_back`, `airbg_sched`. Task 4's scheduler refers to them by those names.

```yaml
# Production deployment for airbg.org. One VPS, one environment.
#
# The security properties of this file are asserted by compose_test.go in this
# directory; read it before changing anything here.
#
# The application publishes NO port, in this file or any other. It is reachable
# only from caddy across the edge network. Direct access for development is an
# SSH local forward to the container address (docs/deployment.md), never a
# published port — so there is no line here to accidentally ship.
name: airbg

services:
  # The only service the internet can open a socket to. Two vhosts: airbg.org,
  # which requires Cloudflare's client certificate, and tiles.airbg.org, which
  # is public. See Caddyfile.
  caddy:
    image: caddy:2.10-alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - /srv/airbg/tls:/etc/caddy/tls:ro
      - caddy_data:/data
      - caddy_config:/config
    networks:
      edge: {}
    depends_on:
      - app
    logging: &journal
      driver: json-file
      options:
        max-size: "10m"
        max-file: "5"

  app:
    image: airbg:${AIRBG_IMAGE_TAG}
    restart: unless-stopped
    env_file: [.env]
    # Binding 0.0.0.0 inside this container is correct AND safe: nothing is
    # published, so the only reachable route is caddy across edge.
    # ipv4_address is pinned so the SSH forward in docs/deployment.md has a
    # stable target; Docker's default allocator would move it on every up.
    networks:
      edge:
        ipv4_address: 172.28.0.10
      back:
        ipv4_address: 172.29.0.10
    volumes:
      - /var/lib/airbg/tiles:/var/lib/airbg/tiles:ro
    depends_on:
      db:
        condition: service_healthy
    read_only: true
    cap_drop: ["ALL"]
    security_opt: ["no-new-privileges:true"]
    logging: *journal

  db:
    image: timescale/timescaledb-ha:pg18
    restart: unless-stopped
    environment:
      - POSTGRES_USER=${POSTGRES_USER}
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=${POSTGRES_DB}
    volumes:
      - db_data:/home/postgres/pgdata
    networks:
      back: {}
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"]
      interval: 10s
      timeout: 5s
      retries: 10
    logging: *journal

networks:
  # Subnets are explicit so AIRBG_LISTEN_TRUSTED_PROXY_CIDRS is a value you can
  # write down. Trusting the wrong CIDR means CF-Connecting-IP is ignored and
  # every visitor shares one rate-limit bucket.
  edge:
    ipam:
      config:
        - subnet: 172.28.0.0/24
  back:
    internal: true
    ipam:
      config:
        - subnet: 172.29.0.0/24

volumes:
  db_data:
  caddy_data:
  caddy_config:
```

- [ ] **Step 6: Run the test and watch it pass**

```bash
go test ./deploy/ -v 2>&1 | tail -15
```

Expected: three PASS lines, `ok  	airbg.org/deploy`.

- [ ] **Step 7: Verify Compose itself accepts the file**

```bash
cd deploy && AIRBG_IMAGE_TAG=test POSTGRES_USER=u POSTGRES_PASSWORD=p POSTGRES_DB=d \
  docker compose -f docker-compose.prod.yml --env-file /dev/null config >/dev/null && echo COMPOSE_OK; cd ..
```

Expected: `COMPOSE_OK`. A schema error here means the file is invalid even though the Go test passed — the test checks security facts, not syntax.

Then check the hardening flags are ones this image actually tolerates. `read_only: true` and `cap_drop: ALL` are asserted here, not proven; a Go binary that needs a writable `/tmp` fails at runtime, not at `compose config`:

```bash
docker run --rm --read-only --cap-drop ALL \
  -e AIRBG_CONFIG=/etc/airbg/airbg.yaml airbg:$(git rev-parse --short HEAD) validate-config
```

If it fails on a read-only filesystem, add `tmpfs: ["/tmp"]` to the `app` service rather than dropping `read_only`. If the image is not built yet, run this at first deploy and record the result in `docs/deployment.md`.

- [ ] **Step 8: Mutation-prove all three tests**

Each mutation is run live, the failure observed, then restored with `cp`.

```bash
cp deploy/docker-compose.prod.yml "$CLAUDE_JOB_DIR/tmp/compose.bak"

# M1: publish the app port — the exact regression the design exists to prevent.
perl -0pi -e 's/  app:\n    image:/  app:\n    ports: ["8080:8080"]\n    image:/' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -5   # expect TestAppPublishesNoPort and TestOnlyCaddyPublishesPorts to FAIL
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

# M2: put the database on the edge network.
perl -0pi -e 's/    networks:\n      back: \{\}\n    healthcheck:/    networks:\n      back: {}\n      edge: {}\n    healthcheck:/' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -5   # expect TestDatabaseIsNotReachable to FAIL
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

# M3: widen caddy's published ports.
perl -pi -e 's/      - "443:443"/      - "443:443"\n      - "9090:9090"/' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -5   # expect TestOnlyCaddyPublishesPorts to FAIL
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

go test ./deploy/ 2>&1 | tail -3   # green again
```

If any mutation does **not** fail its test, the test is inert — fix the test, not the mutation. That failure mode is this project's recurring defect class.

- [ ] **Step 9: Commit**

```bash
gofmt -l deploy/
git add deploy/compose_test.go deploy/docker-compose.prod.yml
git commit -m "deploy: add the production compose file and its port invariants

The app publishes no port in any file: it is reachable only from caddy across
an internal network, which is what keeps a direct-to-origin scraper from
forging CF-Connecting-IP. compose_test.go asserts that, plus the database's
isolation and caddy's exact port set, because these are file-level facts that
rot without noise."
```

---

### Task 3: The trusted-proxy CIDR and its `.env.example`

`listen.trusted_proxy_cidrs` must name the `edge` subnet, because the direct peer is the `caddy` container. Naming Cloudflare's published ranges instead — the intuitive mistake — means `CF-Connecting-IP` is never believed and every visitor shares one rate-limit bucket. The test ties the documented value to the actual subnet so the two cannot drift.

**Files:**
- Create: `deploy/.env.example`
- Modify: `deploy/compose_test.go` (add one test)

**Interfaces:**
- Consumes: `loadCompose`, `composeFile`, `composeNetwork` from Task 2.
- Produces: `deploy/.env.example`, referenced by `docs/deployment.md` in Task 7.

- [ ] **Step 1: Write the failing test**

Append to `deploy/compose_test.go`:

```go
// The trusted-proxy CIDR must be the edge subnet, because the direct peer is
// the caddy container. Cloudflare's published ranges never appear as a peer
// address in this topology, and trusting them instead would silently collapse
// every visitor into one rate-limit bucket. This ties the documented value to
// the real subnet so the two cannot drift apart.
func TestTrustedProxyCIDRMatchesTheEdgeSubnet(t *testing.T) {
	c := loadCompose(t)
	edge, ok := c.Networks["edge"]
	if !ok {
		t.Fatal("no edge network in docker-compose.prod.yml")
	}
	if len(edge.IPAM.Config) != 1 {
		t.Fatalf("edge declares %d ipam configs, want exactly 1 — the subnet must be explicit, not allocator-assigned", len(edge.IPAM.Config))
	}
	subnet := edge.IPAM.Config[0].Subnet

	data, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatalf("ReadFile(.env.example) error = %v, want nil", err)
	}
	const key = "AIRBG_LISTEN_TRUSTED_PROXY_CIDRS="
	var documented string
	var found bool
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, key) {
			documented, found = strings.TrimSpace(strings.TrimPrefix(line, key)), true
			break
		}
	}
	if !found {
		t.Fatalf(".env.example documents no %s line", key)
	}
	if documented != subnet {
		t.Errorf(".env.example says %s%s, but the edge subnet is %s", key, documented, subnet)
	}
}
```

Add `"strings"` to the import block.

- [ ] **Step 2: Run the test and watch it fail**

```bash
go test ./deploy/ -run TestTrustedProxyCIDR 2>&1 | tail -5
```

Expected: FAIL — `ReadFile(.env.example) error = open .env.example: no such file or directory`.

- [ ] **Step 3: Write `.env.example`**

Create `deploy/.env.example`. It carries names and reasons, never values that are secret.

```bash
# Production environment for airbg.org. Copy to /srv/airbg/.env on the host,
# chmod 600, root-owned. Never commit a filled-in copy.
#
# The image carries a complete airbg.yaml; everything here is an override for
# what differs in production. There is deliberately no second config file: a
# forked airbg.prod.yaml drifts silently the day a key is added or renamed.

# The image tag to run. The git sha, so `docker ps` answers "which commit".
AIRBG_IMAGE_TAG=

# The only secret. Env-only by design: writing it into airbg.yaml is a startup
# error, not an ignored value. sslmode=disable is acceptable ONLY because this
# connection never leaves the internal compose network.
AIRBG_DATABASE_URL=postgres://airbg:CHANGE_ME@db:5432/airbg?sslmode=disable

# Consumed by the timescaledb image; must agree with AIRBG_DATABASE_URL.
POSTGRES_USER=airbg
POSTGRES_PASSWORD=CHANGE_ME
POSTGRES_DB=airbg

# Reachable from caddy, safe because the container publishes no port.
AIRBG_LISTEN_ADDR=0.0.0.0:8080
# Metrics on the internal network only. Never published; read with a throwaway
# container attached to airbg_back, since the image has no shell.
AIRBG_LISTEN_METRICS_ADDR=0.0.0.0:9090
# Canonical, hreflang and language-switcher URLs are built from this.
AIRBG_LISTEN_BASE_URL=https://airbg.org
# The edge subnet — the caddy container is the direct peer. NOT Cloudflare's
# published ranges: those never appear as a peer address here, and trusting
# them would mean CF-Connecting-IP is ignored and every visitor shares one
# rate-limit bucket. compose_test.go pins this to the subnet in the compose file.
AIRBG_LISTEN_TRUSTED_PROXY_CIDRS=172.28.0.0/24
# The shipped policy plus the tiles origin in connect-src. Startup FAILS if
# tiles.public_url is absent from connect-src, because a CSP-blocked fetch is a
# blank map and no server-side error anywhere.
AIRBG_LISTEN_CSP=default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self' https://tiles.airbg.org; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'

# The tiles listener, fronted by caddy on the DNS-only tiles hostname.
AIRBG_TILES_ADDR=0.0.0.0:8082
AIRBG_TILES_DIR=/var/lib/airbg/tiles
AIRBG_TILES_PUBLIC_URL=https://tiles.airbg.org
# Regenerating the basemap MUST change this filename: responses are cached
# immutably for a year, so a reused name means returning visitors keep serving
# themselves the old map with no way to invalidate it. See docs/tiles.md.
AIRBG_TILES_ARCHIVE=bulgaria-20260815.pmtiles
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
go test ./deploy/ 2>&1 | tail -5
```

Expected: `ok  	airbg.org/deploy`.

- [ ] **Step 5: Verify the CSP override is one the server accepts**

The CSP value is the one setting here that the binary validates and rejects. Prove it starts rather than assuming:

```bash
AIRBG_CONFIG=airbg.yaml \
AIRBG_DATABASE_URL='postgres://u:p@localhost:5432/d?sslmode=disable' \
AIRBG_LISTEN_CSP="$(grep '^AIRBG_LISTEN_CSP=' deploy/.env.example | cut -d= -f2-)" \
AIRBG_TILES_ADDR=0.0.0.0:8082 \
AIRBG_TILES_DIR=/tmp/does-not-need-to-exist-for-config-validation \
AIRBG_TILES_PUBLIC_URL=https://tiles.airbg.org \
AIRBG_TILES_ARCHIVE=bulgaria-20260815.pmtiles \
go run ./cmd/airbg validate-config
```

Expected: exits 0, or fails **only** on the tiles directory not existing. If it complains about `connect-src`, the CSP line and `AIRBG_TILES_PUBLIC_URL` disagree — fix `.env.example`, not the validator.

- [ ] **Step 6: Mutation-prove the CIDR tie**

```bash
cp deploy/.env.example "$CLAUDE_JOB_DIR/tmp/env.bak"

# M4: the intuitive mistake — trust Cloudflare's ranges instead of the peer.
perl -pi -e 's|^AIRBG_LISTEN_TRUSTED_PROXY_CIDRS=.*|AIRBG_LISTEN_TRUSTED_PROXY_CIDRS=173.245.48.0/20|' deploy/.env.example
go test ./deploy/ -run TestTrustedProxyCIDR 2>&1 | tail -5   # expect FAIL
cp "$CLAUDE_JOB_DIR/tmp/env.bak" deploy/.env.example

# M5: drop the explicit subnet, letting Docker's allocator choose.
cp deploy/docker-compose.prod.yml "$CLAUDE_JOB_DIR/tmp/compose.bak"
perl -0pi -e 's/  edge:\n    ipam:\n      config:\n        - subnet: 172\.28\.0\.0\/24/  edge: {}/' deploy/docker-compose.prod.yml
go test ./deploy/ -run TestTrustedProxyCIDR 2>&1 | tail -5   # expect FAIL on "want exactly 1"
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

go test ./deploy/ 2>&1 | tail -3   # green again
```

- [ ] **Step 7: Commit**

```bash
gofmt -l deploy/
git add deploy/.env.example deploy/compose_test.go
git commit -m "deploy: document the production environment and pin the trusted-proxy CIDR

The trusted CIDR is the edge subnet, because the direct peer is the reverse
proxy container. Naming Cloudflare's published ranges is the intuitive mistake
and it silently collapses every visitor into one rate-limit bucket, so the test
ties the documented value to the subnet the compose file actually creates."
```

---

### Task 4: The scheduler, without handing anything the Docker socket

`airbg collect` is a one-shot pass. Scheduling it inside Compose means a scheduler that can start containers, and that means the Docker socket — which is root-equivalent on the host. `ofelia` therefore never sees the real socket: a proxy in front of it exposes only container create/start, on a network carrying nothing else.

**Files:**
- Modify: `deploy/docker-compose.prod.yml` (add `socket-proxy`, `ofelia`, the `sched` network)
- Create: `deploy/ofelia.ini`
- Modify: `deploy/compose_test.go` (add two tests)

**Interfaces:**
- Consumes: `loadCompose`, `service`, `composeFile` from Task 2.
- Produces: the `sched` network and the two scheduler services; `deploy/ofelia.ini` referenced by `docs/deployment.md` in Task 7.

- [ ] **Step 1: Write the failing tests**

Append to `deploy/compose_test.go`:

```go
// The scheduler is the one component that must be able to start containers,
// and /var/run/docker.sock is root-equivalent on the host: whatever holds it
// can start a privileged container that mounts /. So ofelia talks to a proxy
// that exposes container creation and nothing else, and the proxy is the only
// thing anywhere in the deployment that touches the real socket.
func TestOnlyTheSocketProxyHoldsTheDockerSocket(t *testing.T) {
	c := loadCompose(t)
	for name, svc := range c.Services {
		for _, v := range svc.Volumes {
			if !strings.Contains(v, "docker.sock") {
				continue
			}
			if name != "socket-proxy" {
				t.Errorf("service %s mounts the docker socket (%q); only socket-proxy may", name, v)
			}
			if !strings.HasSuffix(v, ":ro") {
				t.Errorf("service %s mounts the docker socket %q writable, want :ro", name, v)
			}
		}
	}
}

// A socket proxy is only worth having while it stays narrow. These are the
// endpoint groups that would turn it back into a root-equivalent socket:
// EXEC lets you run commands in the running app container, VOLUMES and IMAGES
// let you stage a payload, SWARM and SYSTEM reconfigure the daemon itself.
func TestSocketProxyGrantsOnlyContainerCreation(t *testing.T) {
	proxy := service(t, loadCompose(t), "socket-proxy")
	env := map[string]string{}
	for _, e := range proxy.Environment {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			env[k] = v
		}
	}
	for _, required := range []string{"CONTAINERS", "POST"} {
		if env[required] != "1" {
			t.Errorf("socket-proxy sets %s=%q, want \"1\" — ofelia cannot start jobs without it", required, env[required])
		}
	}
	for _, forbidden := range []string{"EXEC", "IMAGES", "NETWORKS", "VOLUMES", "INFO", "SWARM", "SYSTEM"} {
		if v, set := env[forbidden]; set && v != "0" {
			t.Errorf("socket-proxy sets %s=%q, which widens it back towards a root-equivalent socket", forbidden, v)
		}
	}
}

// The scheduler pair is quarantined: it cannot reach the internet-facing
// network or the database network, and the app cannot reach the scheduler's.
func TestSchedulerIsQuarantined(t *testing.T) {
	c := loadCompose(t)
	for _, name := range []string{"ofelia", "socket-proxy"} {
		svc := service(t, c, name)
		for _, forbidden := range []string{"edge", "back"} {
			if _, on := svc.Networks[forbidden]; on {
				t.Errorf("%s is attached to the %s network, want sched only", name, forbidden)
			}
		}
	}
	if _, on := service(t, c, "app").Networks["sched"]; on {
		t.Error("app is attached to the sched network, want edge and back only")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

```bash
go test ./deploy/ -run 'TestOnlyTheSocketProxy|TestSocketProxyGrants|TestSchedulerIsQuarantined' 2>&1 | tail -10
```

Expected: FAIL — `no service "socket-proxy" in docker-compose.prod.yml`.

- [ ] **Step 3: Add the scheduler services to the Compose file**

Insert before the `networks:` block in `deploy/docker-compose.prod.yml`:

```yaml
  # ofelia needs to start containers, which means the Docker socket, which is
  # root-equivalent on the host. It never gets it: this proxy holds the socket
  # read-only and exposes container creation and nothing else. Widening the
  # environment below re-creates the risk the proxy exists to remove —
  # compose_test.go fails if it happens.
  socket-proxy:
    image: tecnativa/docker-socket-proxy:0.3.0
    restart: unless-stopped
    environment:
      - CONTAINERS=1
      - POST=1
      - EXEC=0
      - IMAGES=0
      - NETWORKS=0
      - VOLUMES=0
      - INFO=0
      - SWARM=0
      - SYSTEM=0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks:
      sched: {}
    read_only: true
    cap_drop: ["ALL"]
    security_opt: ["no-new-privileges:true"]
    logging: *journal

  # Runs `airbg collect` on the poll interval and pg_dump nightly, each as a
  # one-shot container from the same image. Talks to the proxy, never to the
  # daemon.
  ofelia:
    image: mcuadros/ofelia:v0.3.20
    restart: unless-stopped
    command: ["daemon", "--config=/etc/ofelia/config.ini"]
    environment:
      - DOCKER_HOST=tcp://socket-proxy:2375
    volumes:
      - ./ofelia.ini:/etc/ofelia/config.ini:ro
    networks:
      sched: {}
    depends_on:
      - socket-proxy
    logging: *journal
```

And add the third network under `networks:`:

```yaml
  sched:
    internal: true
    ipam:
      config:
        - subnet: 172.30.0.0/24
```

- [ ] **Step 4: Write the schedule**

Create `deploy/ofelia.ini`. Networks are named `airbg_back` because the Compose file sets `name: airbg`.

```ini
; Scheduled jobs for airbg.org. Each runs as a one-shot container; ofelia
; itself holds no application code and no database credentials beyond what it
; passes through.
;
; job-run starts a NEW container per invocation rather than exec-ing into the
; running one. That is deliberate: the app image has no shell, the serving
; container is read-only, and a collector crash must never take the site down.

[job-run "collect"]
schedule = @every 5m
image = airbg:latest
network = airbg_back
command = collect
environment = AIRBG_CONFIG=/etc/airbg/airbg.yaml
delete = true

; pg_dump runs from the database image, which has the client tools and a
; shell. -Fc is the custom format: compressed, and restorable selectively.
[job-run "backup"]
schedule = 0 30 3 * * *
image = timescale/timescaledb-ha:pg18
network = airbg_back
volume = /var/backups/airbg:/backups
command = sh -c "pg_dump -Fc -h db -U $POSTGRES_USER $POSTGRES_DB > /backups/airbg-$(date +%%Y%%m%%d).dump"
delete = true

; Retention. Without this the backup job fills the disk, which is the same
; denial of service the rate limiters exist to prevent, arriving by a
; different door.
[job-run "backup-prune"]
schedule = 0 45 3 * * *
image = timescale/timescaledb-ha:pg18
network = airbg_back
volume = /var/backups/airbg:/backups
command = sh -c "find /backups -name 'airbg-*.dump' -mtime +14 -delete"
delete = true
```

Two host-side prerequisites this creates, both recorded in `docs/deployment.md` in Task 7: `/var/backups/airbg` must exist, and `AIRBG_IMAGE_TAG` must also be tagged `airbg:latest` on the box (or the `image =` lines edited per release) — ofelia's config is read at start, so it cannot interpolate the current tag.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
go test ./deploy/ 2>&1 | tail -5
cd deploy && AIRBG_IMAGE_TAG=test POSTGRES_USER=u POSTGRES_PASSWORD=p POSTGRES_DB=d \
  docker compose -f docker-compose.prod.yml --env-file /dev/null config >/dev/null && echo COMPOSE_OK; cd ..
```

Expected: `ok  	airbg.org/deploy` and `COMPOSE_OK`.

- [ ] **Step 6: Mutation-prove all three tests**

```bash
cp deploy/docker-compose.prod.yml "$CLAUDE_JOB_DIR/tmp/compose.bak"

# M6: hand ofelia the real socket — the shortcut every ofelia tutorial shows.
perl -0pi -e 's|    volumes:\n      - \./ofelia\.ini:/etc/ofelia/config\.ini:ro|    volumes:\n      - ./ofelia.ini:/etc/ofelia/config.ini:ro\n      - /var/run/docker.sock:/var/run/docker.sock|' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -8   # expect TestOnlyTheSocketProxyHoldsTheDockerSocket to FAIL twice: wrong service, and writable
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

# M7: widen the proxy to allow exec into the running app container.
perl -pi -e 's/      - EXEC=0/      - EXEC=1/' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -5   # expect TestSocketProxyGrantsOnlyContainerCreation to FAIL
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

# M8: let the scheduler onto the database network.
perl -0pi -e 's|    networks:\n      sched: \{\}\n    depends_on:\n      - socket-proxy|    networks:\n      sched: {}\n      back: {}\n    depends_on:\n      - socket-proxy|' deploy/docker-compose.prod.yml
go test ./deploy/ 2>&1 | tail -5   # expect TestSchedulerIsQuarantined to FAIL
cp "$CLAUDE_JOB_DIR/tmp/compose.bak" deploy/docker-compose.prod.yml

go test ./deploy/ 2>&1 | tail -3   # green again
```

- [ ] **Step 7: Commit**

```bash
gofmt -l deploy/
git add deploy/docker-compose.prod.yml deploy/ofelia.ini deploy/compose_test.go
git commit -m "deploy: schedule collect and backups without exposing the Docker socket

The Docker socket is root-equivalent on the host, so ofelia never receives it:
a proxy holds it read-only and exposes container creation alone, on a network
carrying nothing else. The tests fail if any other service mounts the socket,
if the proxy is widened towards exec or image control, or if the scheduler
gains a route to the app or the database."
```

---

### Task 5: Caddy — the certificate requirement that keeps the origin private

`airbg.org` requires a TLS client certificate that only Cloudflare's edge holds, so a direct connection to the origin IP fails the handshake regardless of source address. `tiles.airbg.org` shares the port and stays public, which a packet filter could not arrange: SNI is above the layer it works at.

**Files:**
- Create: `deploy/Caddyfile`
- Modify: `deploy/compose_test.go` (add one test)

**Interfaces:**
- Consumes: nothing from Tasks 2–4 except that `deploy/docker-compose.prod.yml` already mounts `./Caddyfile` and `/srv/airbg/tls`.
- Produces: `deploy/Caddyfile`, referenced by `docs/deployment.md` in Task 7.

- [ ] **Step 1: Write the failing test**

A Caddyfile has its own syntax and no Go parser is available under the no-new-dependency rule, so this is a text assertion. Being a text assertion, it must be written to survive the mutation that a naive whole-file substring check would miss: `client_auth` present, but in the wrong site block.

Append to `deploy/compose_test.go`:

```go
// caddyBlocks splits a Caddyfile into site blocks keyed by their header line.
// Deliberately simple: site headers start at column 0 and end with " {", and
// the matching close is a "}" at column 0. That is the shape of this file, and
// the test fails loudly if it stops being.
func caddyBlocks(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatalf("ReadFile(Caddyfile) error = %v, want nil", err)
	}
	blocks := map[string]string{}
	var current string
	var body []string
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case current == "" && strings.HasSuffix(line, " {") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			current = strings.TrimSuffix(line, " {")
			body = nil
		case current != "" && line == "}":
			blocks[current] = strings.Join(body, "\n")
			current = ""
		case current != "":
			body = append(body, line)
		}
	}
	if current != "" {
		t.Fatalf("Caddyfile block %q is never closed at column 0", current)
	}
	return blocks
}

// This is the whole enforcement. Cloudflare's edge holds a client certificate
// the public does not, so a direct connection to the origin IP fails the TLS
// handshake and no request carrying a forged CF-Connecting-IP ever reaches the
// rate limiters. The tiles host shares the port and must NOT require it —
// browsers connect to it directly.
//
// Checked per block, not per file: `client_auth` in the tiles block would
// satisfy a whole-file substring check while leaving the API wide open.
func TestOnlyTheSiteVhostRequiresCloudflaresCertificate(t *testing.T) {
	blocks := caddyBlocks(t)

	site, ok := blocks["airbg.org"]
	if !ok {
		t.Fatalf("Caddyfile has no airbg.org site block; found %v", keysOf(blocks))
	}
	if !strings.Contains(site, "client_auth") {
		t.Error("the airbg.org block does not require a client certificate — the origin is reachable directly")
	}
	if !strings.Contains(site, "require_and_verify") {
		t.Error("the airbg.org block does not use require_and_verify; any weaker mode accepts a connection with no certificate")
	}

	tiles, ok := blocks["tiles.airbg.org"]
	if !ok {
		t.Fatalf("Caddyfile has no tiles.airbg.org site block; found %v", keysOf(blocks))
	}
	if strings.Contains(tiles, "client_auth") {
		t.Error("the tiles block requires a client certificate; browsers connect to it directly and would all fail")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Add `"sort"` to the import block.

- [ ] **Step 2: Run the test and watch it fail**

```bash
go test ./deploy/ -run TestOnlyTheSiteVhost 2>&1 | tail -5
```

Expected: FAIL — `ReadFile(Caddyfile) error = open Caddyfile: no such file or directory`.

- [ ] **Step 3: Write the Caddyfile**

Create `deploy/Caddyfile`:

```
# Two vhosts on one port, with opposite trust rules — which is exactly why the
# enforcement is a certificate and not a packet filter. A filter cannot tell
# these apart: SNI is above the layer it operates at.
#
# deploy/compose_test.go asserts the client_auth block below is present, is
# require_and_verify, and is in THIS block rather than the tiles one.

{
	# No ACME account email is configured for the site vhost: its certificate
	# comes from Cloudflare's origin CA, not from Let's Encrypt. The tiles
	# vhost below does use ACME, so an address is required for expiry notices.
	email admin@airbg.org
}

airbg.org {
	# The Cloudflare Origin CA certificate is trusted by Cloudflare and by
	# nothing else — correct here precisely because only Cloudflare ever
	# connects. client_auth is the other half: Cloudflare's edge presents a
	# certificate the public does not have, so a direct connection to the
	# origin IP fails the handshake regardless of its source address.
	tls /etc/caddy/tls/origin.pem /etc/caddy/tls/origin.key {
		client_auth {
			mode require_and_verify
			trust_pool file /etc/caddy/tls/cloudflare-origin-pull-ca.pem
		}
	}

	# CF-Connecting-IP passes through untouched: it is the rate-limit bucket
	# key, and the app trusts it only from this container's subnet.
	reverse_proxy app:8080
}

tiles.airbg.org {
	# DNS-only (grey cloud) per docs/tiles.md: browsers connect straight here,
	# so this needs a publicly trusted certificate and must NOT require a
	# client one. Caddy obtains and renews it automatically.
	reverse_proxy app:8082
}
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
go test ./deploy/ 2>&1 | tail -5
```

Expected: `ok  	airbg.org/deploy`.

- [ ] **Step 5: Verify the syntax against the real Caddy, not against belief**

`client_auth` sub-directive names changed across Caddy 2.x (`trusted_ca_cert_file` in older versions, `trust_pool file` from 2.7). The image is pinned to `caddy:2.10-alpine`; confirm this file is valid for it:

```bash
docker run --rm -v "$PWD/deploy/Caddyfile:/etc/caddy/Caddyfile:ro" caddy:2.10-alpine \
  caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile 2>&1 | tail -20
```

Expected: `Valid configuration`. If it reports an unknown subdirective, fix the Caddyfile to match this image's syntax and re-run the Go test — the test asserts `client_auth` and `require_and_verify`, both of which survive every 2.x spelling of the surrounding block. Do not change the pinned image to match stale syntax.

- [ ] **Step 6: Mutation-prove the test, including the wrong-block case**

```bash
cp deploy/Caddyfile "$CLAUDE_JOB_DIR/tmp/Caddyfile.bak"

# M9: drop the requirement entirely — the origin becomes publicly reachable.
perl -0pi -e 's/\t\tclient_auth \{\n\t\t\tmode require_and_verify\n\t\t\ttrust_pool file \/etc\/caddy\/tls\/cloudflare-origin-pull-ca\.pem\n\t\t\}\n//' deploy/Caddyfile
go test ./deploy/ -run TestOnlyTheSiteVhost 2>&1 | tail -5   # expect FAIL on both client_auth and require_and_verify
cp "$CLAUDE_JOB_DIR/tmp/Caddyfile.bak" deploy/Caddyfile

# M10: weaken the mode. Anything but require_and_verify accepts a connection
# that presents no certificate at all.
perl -pi -e 's/mode require_and_verify/mode request/' deploy/Caddyfile
go test ./deploy/ -run TestOnlyTheSiteVhost 2>&1 | tail -5   # expect FAIL
cp "$CLAUDE_JOB_DIR/tmp/Caddyfile.bak" deploy/Caddyfile

# M11: the one a whole-file substring check would miss — the requirement is
# present, but guarding the wrong host.
perl -0pi -e 's/\t\tclient_auth \{\n\t\t\tmode require_and_verify\n\t\t\ttrust_pool file \/etc\/caddy\/tls\/cloudflare-origin-pull-ca\.pem\n\t\t\}\n//' deploy/Caddyfile
perl -0pi -e 's|tiles\.airbg\.org \{|tiles.airbg.org {\n\ttls {\n\t\tclient_auth {\n\t\t\tmode require_and_verify\n\t\t\ttrust_pool file /etc/caddy/tls/cloudflare-origin-pull-ca.pem\n\t\t}\n\t}|' deploy/Caddyfile
go test ./deploy/ -run TestOnlyTheSiteVhost 2>&1 | tail -6   # expect FAIL on the airbg.org block AND on the tiles block
cp "$CLAUDE_JOB_DIR/tmp/Caddyfile.bak" deploy/Caddyfile

go test ./deploy/ 2>&1 | tail -3   # green again
```

M11 is the important one: if it does not fail, the test is a substring check wearing a parser's clothes.

- [ ] **Step 7: Commit**

```bash
gofmt -l deploy/
git add deploy/Caddyfile deploy/compose_test.go
git commit -m "deploy: require Cloudflare's client certificate on the site vhost

Authenticated Origin Pulls is the enforcement: Cloudflare's edge holds a
certificate the public does not, so a direct connection to the origin IP fails
the handshake whatever its source address. An IP allowlist could not do this
job — tiles.airbg.org shares port 443 and must stay public, and a packet filter
cannot tell two hostnames apart.

The test checks the requirement per site block, because client_auth in the
tiles block would satisfy a whole-file substring check while leaving the API
open."
```

---

### Task 6: The host firewall and closing out `docs/tiles.md`

nftables is the outer layer, not the enforcement. It exists so that something later binding a port by accident is not immediately reachable.

**Files:**
- Create: `deploy/nftables.conf`
- Modify: `docs/tiles.md` (§6 and the "Open deployment questions" section)

**Interfaces:**
- Consumes: nothing.
- Produces: `deploy/nftables.conf`, installed by the runbook in Task 7.

- [ ] **Step 1: Write the ruleset**

Create `deploy/nftables.conf`:

```
#!/usr/sbin/nft -f
# Outer layer only. What actually keeps scrapers off the API is the client
# certificate Caddy requires on the airbg.org vhost (deploy/Caddyfile) — this
# ruleset cannot do that job, because tiles.airbg.org shares port 443 and must
# stay public, and a packet filter cannot distinguish two hostnames.
#
# What this DOES do: ensure that anything binding a port by accident is not
# immediately reachable from the internet.
#
# Docker manages its own chains and its published ports bypass the filter
# table's input chain, which is why this is a floor, not a ceiling.

flush ruleset

table inet filter {
	chain input {
		type filter hook input priority filter; policy drop;

		ct state established,related accept
		ct state invalid drop
		iif lo accept

		ip protocol icmp accept
		ip6 nexthdr ipv6-icmp accept

		tcp dport 22 accept comment "ssh — also the only route to the app, via local forward"
		tcp dport 80 accept comment "ACME http-01 for tiles.airbg.org"
		tcp dport 443 accept comment "caddy: both vhosts"
	}

	chain forward {
		type filter hook forward priority filter; policy accept;
		comment "Docker manages forwarding; do not fight it here"
	}

	chain output {
		type filter hook output priority filter; policy accept;
	}
}
```

- [ ] **Step 2: Verify the ruleset parses**

```bash
docker run --rm -v "$PWD/deploy/nftables.conf:/etc/nftables.conf:ro" \
  --entrypoint sh alpine:3.20 -c "apk add --no-cache nftables >/dev/null && nft --check --file /etc/nftables.conf && echo NFT_OK"
```

Expected: `NFT_OK`. A syntax error here becomes a locked-out VPS at install time, so this check is not optional.

- [ ] **Step 3: Update `docs/tiles.md` §6 to describe the enforcement that actually shipped**

§6 currently says the application port accepts connections only from Cloudflare's ranges, enforced by a packet filter. That is not what shipped. Replace the bullet list in "## 6. The firewall rule" with:

```markdown
- The **application vhost** (`airbg.org`) requires a TLS client certificate
  issued by Cloudflare's origin-pull CA, enforced by Caddy
  (`deploy/Caddyfile`). A direct connection to the origin IP fails the
  handshake whatever its source address. This replaces the IP allowlist this
  section originally proposed: an allowlist trusts where a packet came from,
  and anything hosted inside Cloudflare's ranges qualifies.
- The **tiles vhost** (`tiles.airbg.org`) accepts the world, on a DNS-only
  hostname, with a publicly trusted certificate. It shares port 443 with the
  application vhost — which is why the enforcement has to be per-vhost. SNI is
  above the layer a packet filter works at.
- `listen.trusted_proxy_cidrs` governs header parsing, not who may connect, and
  in this deployment it names the reverse proxy's own Docker subnet — not
  Cloudflare's ranges, which never appear as a peer address.

With this in place, discovering the origin IP yields tiles and a TLS rejection.
```

- [ ] **Step 4: Replace "Open deployment questions" with the decisions**

Both questions are now answered. Replace that whole section with:

```markdown
## Deployment decisions

Both questions this section used to leave open are settled in
`docs/superpowers/specs/2026-08-17-airbg-deployment-design.md`:

- `tiles.dir` is a **bind-mounted host directory** (`/var/lib/airbg/tiles`),
  mounted read-only into the app container. The image stays ~27 MB and
  regenerating the basemap is an scp rather than a rebuild — which matters
  because releases ship the whole image over the wire.
- `tiles.airbg.org` gets **its own Let's Encrypt certificate**, obtained and
  renewed by Caddy. Not a wildcard, and specifically not a Cloudflare Origin CA
  certificate: browsers do not trust one, and this hostname is deliberately not
  proxied.
```

- [ ] **Step 5: Confirm nothing else in the tree still promises the old rule**

```bash
grep -rn "Cloudflare's IP ranges\|Cloudflare's published ranges\|packet filter" docs/ airbg.yaml deploy/ | grep -v deployment-design
```

Expected: only lines that describe the *rejected* approach as rejected. Any line still instructing an operator to allowlist Cloudflare's ranges for the app port is now wrong and must be fixed here.

- [ ] **Step 6: Commit**

```bash
git add deploy/nftables.conf docs/tiles.md
git commit -m "deploy: add the host firewall floor and record the tiles decisions

nftables is the outer layer, not the enforcement — it cannot be, since the
tiles vhost shares port 443 and must stay public. docs/tiles.md's firewall
section said the app port would be IP-allowlisted; that is not what shipped,
so it now describes the certificate requirement that did, and its two open
questions are replaced by the decisions."
```

---

### Task 7: The operator runbook

Everything above is inert without a document that says what to do with it, in what order, and how to know it worked.

**Files:**
- Create: `docs/deployment.md`

**Interfaces:**
- Consumes: every file created in Tasks 2–6.
- Produces: the runbook referenced by `deploy/.env.example` and `docs/tiles.md`.

- [ ] **Step 1: Write the runbook**

Create `docs/deployment.md` with these sections, in this order. Content requirements are given per section — write them out in full, with real commands, not summaries.

1. **What runs where** — the five services, three networks, and the single fact that only Caddy publishes ports. State that the app publishes none, in any environment, and that direct access is an SSH forward.

2. **First-run bootstrap**, numbered, in this exact order:
   1. Provision the host; install Docker; `mkdir -p /srv/airbg /srv/airbg/tls /var/lib/airbg/tiles /var/backups/airbg`.
   2. Install `deploy/nftables.conf` to `/etc/nftables.conf`, `systemctl enable --now nftables`. Warn that SSH must be working on port 22 before this is applied.
   3. Cloudflare DNS: `airbg.org` proxied (orange), `tiles.airbg.org` DNS-only (grey) at the host IP.
   4. Issue a Cloudflare Origin CA certificate for `airbg.org`; save as `/srv/airbg/tls/origin.pem` and `origin.key`. Download Cloudflare's origin-pull CA certificate to `/srv/airbg/tls/cloudflare-origin-pull-ca.pem`. `chown root:root`, `chmod 600`. Enable Authenticated Origin Pulls for the zone in the Cloudflare dashboard. Note that the origin certificate is a 15-year credential living on the box — the one long-lived secret this design adds.
   5. Copy `deploy/.env.example` to `/srv/airbg/.env`, fill it in, `chmod 600`, `chown root:root`.
   6. Copy `deploy/docker-compose.prod.yml`, `Caddyfile` and `ofelia.ini` to `/srv/airbg/`.
   7. Generate and install the tiles artefacts per `docs/tiles.md`; set `AIRBG_TILES_ARCHIVE` to match the filename.
   8. Load the image (see Releases) and tag it `airbg:latest` as well, since `ofelia.ini` cannot interpolate the current tag.
   9. `docker compose run --rm app validate-config`
   10. `docker compose run --rm app migrate`
   11. `docker compose run --rm app import-areas`
   12. `docker compose up -d`

3. **Post-deploy checklist** — each item a command and its expected result:
   - the site answers through Cloudflare: `curl -sS -o /dev/null -w '%{http_code}\n' https://airbg.org/` → `200`
   - **the origin refuses a direct connection**: `curl -sS --resolve airbg.org:443:<origin IP> https://airbg.org/` → TLS handshake failure. This is the assertion the whole design rests on; if it returns a page, stop and fix it before announcing the site.
   - tiles answer directly with a browser-valid certificate: `curl -sSI https://tiles.airbg.org/<archive name> | head -1`
   - metrics are not public: `curl -sS --max-time 5 http://<origin IP>:9090/metrics` → connection refused
   - metrics are readable internally: `docker run --rm --network airbg_back curlimages/curl -sS http://app:9090/metrics | head -5`
   - the collector has run: check `docker logs` for the ofelia job, and that the site shows recent readings

4. **Releases** — the build-and-ship sequence, verbatim:

```bash
TAG=$(git rev-parse --short HEAD)
docker build -t airbg:$TAG .
docker save airbg:$TAG | gzip | ssh airbg 'gunzip | docker load'
ssh airbg "docker tag airbg:$TAG airbg:latest \
  && sed -i 's/^AIRBG_IMAGE_TAG=.*/AIRBG_IMAGE_TAG=$TAG/' /srv/airbg/.env \
  && cd /srv/airbg \
  && docker compose -f docker-compose.prod.yml run --rm app migrate \
  && docker compose -f docker-compose.prod.yml up -d"
```

   State why `migrate` is a separate one-shot: a migration failure must stop the deploy, not crash-loop a serving container. State that rollback is the same sequence with the previous sha, which stays loaded until pruned.

5. **Development access** — the SSH forwards, with both caveats spelled out:

```bash
ssh -L 8080:172.28.0.10:8080 airbg    # browse http://localhost:8080
ssh -L 9090:172.29.0.10:9090 airbg    # metrics
```

   Caveat one: `AIRBG_LISTEN_BASE_URL` is `https://airbg.org`, so canonical, `hreflang` and language-switcher links point at production while you browse the forward; relative navigation behaves normally. Caveat two: the forward bypasses Caddy and Cloudflare, so it does not exercise `CF-Connecting-IP` bucketing, Caddy's headers, or the certificate requirement — verify those against the public URL. State explicitly that no `ports:` line is ever added to get local access, in any environment.

6. **Tiles regeneration** — point at `docs/tiles.md`, and state the three names that must agree (file on disk, `AIRBG_TILES_ARCHIVE`, the `pmtiles://` URL inside `style.json`), and that the filename must change on every regeneration because responses are cached immutably for a year.

7. **Backups and restore** — where dumps land (`/var/backups/airbg/airbg-YYYYMMDD.dump`), the 14-day retention, and the restore command:

```bash
docker run --rm --network airbg_back -v /var/backups/airbg:/backups \
  -e PGPASSWORD=<password> timescale/timescaledb-ha:pg18 \
  pg_restore -h db -U airbg -d airbg --clean --if-exists /backups/airbg-YYYYMMDD.dump
```

   State that the history is not regenerable from sensor.community even though future data is, so this protects history rather than availability.

8. **Adding a language or changing copy** — one paragraph: set `AIRBG_I18N_DIR`, drop a `<lang>.json` in it, restart. No rebuild, no migration. Point at `docs/configuration.md`.

9. **Log rotation and disk** — the `max-size`/`max-file` caps in the Compose file, and that a log flood filling the disk is the same class of denial of service the rate limiters exist to prevent.

- [ ] **Step 2: Verify every command in the runbook is real**

Walk the document and check each command against the tree: subcommand names against `cmd/airbg/main.go`'s usage line (`migrate|collect|serve|backfill|import-areas|purge-outside-boundary|validate-config`), file paths against `deploy/`, network names against `name: airbg` in the Compose file, and the static IPs against `ipv4_address` in the Compose file. A runbook with one wrong command is worse than none, because it is followed under pressure.

- [ ] **Step 3: Commit**

```bash
git add docs/deployment.md
git commit -m "docs: add the deployment runbook

Bootstrap, releases, rollback, dev access, tiles regeneration, backups and
restore. The post-deploy checklist includes the negative test the design rests
on: a direct connection to the origin IP must fail the TLS handshake."
```

---

### Task 8: Whole-tree verification

The deployment artefacts are new files in a Go module. Prove nothing regressed and the new package joins the standard net.

**Files:** none modified — this task only runs things and, if something fails, fixes what it finds.

**Interfaces:**
- Consumes: everything from Tasks 1–7.
- Produces: a verified tree.

- [ ] **Step 1: Format and vet, both tag sets**

```bash
gofmt -l .
go vet ./... && go vet -tags integration ./...
```

Expected: no output from `gofmt`; both vets clean. `go vet ./...` does not compile files behind `//go:build integration`, which is why both are run.

- [ ] **Step 2: Unit tier, and confirm the new package is in it**

```bash
go test ./... 2>&1 | tail -25
```

Expected: every package `ok`, including `airbg.org/deploy`. If `deploy` is absent from the output, the test file is not being picked up — check the package clause.

- [ ] **Step 3: Integration tier**

```bash
go test -tags integration ./... 2>&1 | tail -25
```

Expected: all `ok`. This tier starts real Postgres+PostGIS+Timescale containers, so Docker must be running.

- [ ] **Step 4: Frontend tier**

```bash
cd web && npm test 2>&1 | tail -10; cd ..
```

Expected: all tests pass. This plan does not touch `web/`, so any failure here is pre-existing and must be reported, not absorbed.

- [ ] **Step 5: End-to-end tier**

```bash
cd web && npm run build && cd ..
go test -tags e2e ./internal/e2e/ 2>&1 | tail -10
git checkout -- internal/web/dist/.keep
```

Expected: `ok  	airbg.org/internal/e2e`. `npm run build` deletes `internal/web/dist/.keep`; the last line restores it. This is the one place `git checkout` is correct, because the file is unmodified in git and deleted by the build.

- [ ] **Step 6: Confirm the working tree is clean and CLAUDE.md is not staged**

```bash
git status --short
```

Expected: empty. If `CLAUDE.md` appears, it must not be committed — it is gitignored on purpose.

- [ ] **Step 7: Review the whole branch diff for leaked values**

```bash
git diff master --stat
git diff master -- deploy/ | grep -iE 'password=|secret|token|BEGIN .*PRIVATE KEY' | grep -v CHANGE_ME
```

Expected: the stat covers only `airbg.yaml`, `internal/config/`, `deploy/`, `docs/`; the grep returns nothing. `CHANGE_ME` placeholders in `.env.example` are intended.

---

## Verification summary

| Property | Guarded by |
|---|---|
| App publishes no port, anywhere | `TestAppPublishesNoPort`, `TestOnlyCaddyPublishesPorts` (M1, M3) |
| Database unreachable except from the app | `TestDatabaseIsNotReachable` (M2) |
| Trusted CIDR is the peer's subnet, and stays documented | `TestTrustedProxyCIDRMatchesTheEdgeSubnet` (M4, M5) |
| Only the proxy holds the Docker socket, read-only | `TestOnlyTheSocketProxyHoldsTheDockerSocket` (M6) |
| The socket proxy stays narrow | `TestSocketProxyGrantsOnlyContainerCreation` (M7) |
| The scheduler cannot reach the app or the database | `TestSchedulerIsQuarantined` (M8) |
| Only the site vhost requires Cloudflare's certificate | `TestOnlyTheSiteVhostRequiresCloudflaresCertificate` (M9, M10, M11) |
| The enumeration retune is deliberate, not drift | `TestShippedValuesMatchPhase2Behaviour`, `TestResolve` |
| The origin actually refuses direct connections | The post-deploy checklist's `--resolve` test — a runtime fact no unit test can assert |
