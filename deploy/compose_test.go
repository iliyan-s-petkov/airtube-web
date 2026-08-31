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
	"sort"
	"strings"
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
	Image       string                   `yaml:"image"`
	Ports       []string                 `yaml:"ports"`
	Networks    map[string]composeAttach `yaml:"networks"`
	Environment []string                 `yaml:"environment"`
	Volumes     []string                 `yaml:"volumes"`
	NetworkMode string                   `yaml:"network_mode"`
	ReadOnly    bool                     `yaml:"read_only"`
	Tmpfs       []string                 `yaml:"tmpfs"`
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

// db must not be able to open a connection *outward* either. Reachability and
// egress are separate properties and this one has been lost once already: a
// non-internal `collect` network was added so an ofelia job could reach the
// internet, db was joined to it so the same job could also reach the database,
// and db silently gained real internet access as a side effect. Every network
// db sits on must be internal: true.
func TestDatabaseHasNoRouteToTheInternet(t *testing.T) {
	c := loadCompose(t)
	attached := service(t, c, "db").Networks
	if len(attached) == 0 {
		t.Fatal("db is attached to no network at all")
	}
	for name := range attached {
		net, ok := c.Networks[name]
		if !ok {
			t.Errorf("db is attached to network %q, which is not declared in the networks: section", name)
			continue
		}
		if !net.Internal {
			t.Errorf("db is attached to network %q, which is not internal: true — the database has a route to the public internet", name)
		}
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

// network_mode: host is the port publication that leaves no ports: key behind.
// It would put every listener in the container directly on the host, defeating
// TestAppPublishesNoPort and TestOnlyCaddyPublishesPorts without tripping
// either — they inspect only Ports. No service in this deployment has any
// business using it.
func TestNoServiceUsesHostNetworking(t *testing.T) {
	for name, svc := range loadCompose(t).Services {
		if svc.NetworkMode != "" {
			t.Errorf("service %s sets network_mode: %q; this deployment isolates every service on a Docker network", name, svc.NetworkMode)
		}
	}
}

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
// Each forbidden key must be explicitly "0", not merely absent: the proxy
// image happens to deny undeclared endpoints by default today, but a line
// deleted in a future edit would silently fall back to that default instead
// of failing this test, so the config must say so itself rather than lean on
// an assumption about the image.
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
		v, set := env[forbidden]
		if !set || v != "0" {
			t.Errorf("socket-proxy sets %s=%q (present=%v), want an explicit \"0\" — an absent key relies on the image's default instead of the config saying so", forbidden, v, set)
		}
	}
}

// The app container is the one the internet's traffic reaches, so a write it
// can perform is a write an attacker may be able to direct. It stays
// read_only.
//
// socket-proxy is the documented exception, and this test pins the exception
// so nobody re-adds the hardening in good faith and takes the scheduler down
// with it. Its entrypoint renders haproxy.cfg from a template next to it on
// every start: read_only crash-loops the container, and a tmpfs over
// /usr/local/etc/haproxy crash-loops it too, hiding the image's own template
// so the log says "haproxy.cfg.template: No such file or directory" instead of
// anything about permissions. Both were tried against the running host. The
// properties that actually contain this service are asserted by the two tests
// above and are untouched by it being writable.
func TestAppIsReadOnlyAndTheSocketProxyIsTheDocumentedException(t *testing.T) {
	c := loadCompose(t)
	if !service(t, c, "app").ReadOnly {
		t.Error("app is not read_only; the internet-facing container must not be able to write to its own filesystem")
	}
	proxy := service(t, c, "socket-proxy")
	if proxy.ReadOnly {
		t.Error("socket-proxy is read_only, which crash-loops it: its entrypoint must write haproxy.cfg at startup")
	}
	for _, mount := range proxy.Tmpfs {
		if strings.HasPrefix(mount, "/usr/local/etc/haproxy") {
			t.Errorf("socket-proxy mounts a tmpfs at %q, which hides the image's haproxy.cfg.template and crash-loops it", mount)
		}
	}
}

// The scheduler pair is quarantined: it cannot reach the internet-facing
// network or the database network, and the app cannot reach the scheduler's.
// db must also stay off sched: it is the one service the collect and backup
// jobs must reach across the back network — sched carries only ofelia and
// the socket proxy.
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
	if _, on := service(t, c, "db").Networks["sched"]; on {
		t.Error("db is attached to the sched network, want back only")
	}
}

// stripCaddyComment removes a "#" comment from a Caddyfile line. Caddyfile
// comments start with "#" at the beginning of the line or preceded by
// whitespace; that is enough to handle both whole-line and inline comments
// in this file without needing a real Caddyfile lexer.
func stripCaddyComment(line string) string {
	if idx := strings.Index(line, "#"); idx != -1 && (idx == 0 || line[idx-1] == ' ' || line[idx-1] == '\t') {
		return line[:idx]
	}
	return line
}

// caddyBlocks splits a Caddyfile into site blocks keyed by their header line.
// Deliberately simple: site headers start at column 0 and end with " {", and
// the matching close is a "}" at column 0. That is the shape of this file, and
// the test fails loudly if it stops being.
//
// Comments are stripped from each block's body before it is stored. These
// blocks are heavily commented, and the comments necessarily name the very
// directives this test asserts on (they explain client_auth and
// require_and_verify in prose) — so without stripping them, a check meant to
// match configuration could be satisfied, or defeated, by documentation
// instead. A comment must never be able to stand in for the directive it
// describes.
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
			if stripped := stripCaddyComment(line); strings.TrimSpace(stripped) != "" {
				body = append(body, stripped)
			}
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

// ofeliaJobLines returns every `key = value` line inside a `[job-run "name"]`
// section of ofelia.ini, in file order, with full-line `;` comments and blank
// lines dropped. Deliberately simple, same spirit as caddyBlocks: sections
// start at column 0 with a `[...]` header and run until the next one. A key
// like `environment` or `volume` can appear more than once in a real section
// — ofelia treats repeats of those as a slice, not a last-wins scalar, see
// the comment above [job-run "collect"] in ofelia.ini — so every occurrence
// is returned, not just the last.
func ofeliaJobLines(t *testing.T, header string) []string {
	t.Helper()
	data, err := os.ReadFile("ofelia.ini")
	if err != nil {
		t.Fatalf("ReadFile(ofelia.ini) error = %v, want nil", err)
	}
	want := "[" + header + "]"
	var lines []string
	inSection := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = line == want
			continue
		}
		if inSection {
			lines = append(lines, line)
		}
	}
	if lines == nil {
		t.Fatalf("ofelia.ini has no %s section", want)
	}
	return lines
}

// ofeliaValue returns the value of the first `key = ...` line among lines, or
// "" with ok=false if key never appears.
func ofeliaValue(lines []string, key string) (string, bool) {
	prefix := key + " ="
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", false
}

// The collect job is started fresh through the Docker API by ofelia's
// job-run, which — unlike a Compose service — inherits no env_file and no
// mounts from anything else in this deployment. Without a database
// credential reaching that spawned container, internal/config.Validate makes
// startup fail, silently, every 5 minutes, because delete = true leaves no
// exited container behind to notice. This asserts the credential mechanism
// is actually wired: AIRBG_DATABASE_URL_FILE is set, and a read-only volume
// is bind-mounted at the exact path it names — the two halves of the
// PGPASSFILE-style pattern the backup job already uses.
func TestCollectJobHasADatabaseCredential(t *testing.T) {
	lines := ofeliaJobLines(t, `job-run "collect"`)

	var fileEnv string
	var found bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "environment =") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "environment ="))
		k, val, ok := strings.Cut(v, "=")
		if ok && k == "AIRBG_DATABASE_URL_FILE" {
			fileEnv, found = val, true
		}
	}
	if !found {
		t.Fatal(`collect job sets no environment = AIRBG_DATABASE_URL_FILE=...; the collector has no way to reach the database and dies at startup every run`)
	}

	var mounted bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "volume =") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "volume ="))
		parts := strings.Split(v, ":")
		if len(parts) >= 2 && parts[1] == fileEnv {
			mounted = true
			if !strings.HasSuffix(v, ":ro") {
				t.Errorf("collect job mounts the credential file %q writable, want :ro", v)
			}
		}
	}
	if !mounted {
		t.Errorf("collect job sets AIRBG_DATABASE_URL_FILE=%s but mounts no volume at that path — the file the job expects to read never exists inside the container", fileEnv)
	}
}

// Every job in ofelia.ini names a network that must (a) exist in the compose
// file and (b) be internal: true. (b) is the point. It is tempting to think a
// job needing the internet — collect does, for https://data.sensor.community —
// justifies a non-internal network; it does not, because ofelia attaches every
// job-run container to Docker's default bridge as well, so the job has egress
// regardless. A non-internal network here would buy nothing and would hand
// egress to whichever long-lived service was joined to it to make the job
// reachable — which is exactly how db acquired internet access once already.
func TestEveryOfeliaJobRunsOnAnInternalNetwork(t *testing.T) {
	c := loadCompose(t)
	for _, job := range []string{"collect", "backup", "backup-prune"} {
		lines := ofeliaJobLines(t, `job-run "`+job+`"`)
		network, ok := ofeliaValue(lines, "network")
		if !ok {
			t.Errorf("%s job sets no network =; ofelia would attach it to the Docker default bridge only, so it could not reach db", job)
			continue
		}
		name := strings.TrimPrefix(network, "airbg_")
		net, ok := c.Networks[name]
		if !ok {
			t.Errorf("%s job's network = %s does not correspond to any network in docker-compose.prod.yml (looked for %q)", job, network, name)
			continue
		}
		if !net.Internal {
			t.Errorf("%s job runs on network %s, which is not internal: true — a job-run container already gets internet from Docker's default bridge, so the only thing a non-internal network here adds is egress for the services joined to it", job, network)
		}
	}
}

// A failing backup is silent by construction: the backup job's `&&`
// short-circuits, delete = true removes the exited container before anyone
// could read its status, and last night's dump stays where it was — so the
// backup directory keeps looking healthy while its newest file quietly stops
// advancing. On a single VPS with no replica that is discovered during a
// restore, which is the worst possible moment.
//
// The prune job carries the alarm because it already runs daily against the
// same directory. This asserts all four moving parts, because dropping any one
// of them restores the silence: the freshness window, the marker file an
// operator sees by running ls, the non-zero exit that reaches ofelia's log,
// and the cleanup that lets the marker clear itself once backups resume.
//
// The glob check is not style. A bare airbg-*.dump is expanded by sh against
// the container's working directory before find runs; one stray file of that
// shape rewrites the search into a single literal name that matches nothing,
// and the staleness check then fires every night against a healthy backup
// directory. It cannot be fixed with quotes — see the no-double-quote test
// below — and it cannot be fixed with a backslash either: ofelia's INI parser
// rejects the file outright with "unquoted '\' must be followed by new line or
// double quote", so the daemon never starts and NO job runs. `set -f` is the
// one escape that survives both parsers, because it is plain words.
func TestBackupPruneAlarmsWhenBackupsGoStale(t *testing.T) {
	lines := ofeliaJobLines(t, `job-run "backup-prune"`)
	command, ok := ofeliaValue(lines, "command")
	if !ok {
		t.Fatal(`backup-prune job has no command =`)
	}

	for _, part := range []struct{ substr, why string }{
		{`-mtime -2`, "no freshness window: nothing detects that the newest dump has stopped advancing"},
		{`BACKUP-IS-STALE`, "no marker file: the only signal would be ofelia's log, not the directory an operator actually looks at"},
		{`exit 1`, "no non-zero exit: the failure never reaches ofelia's log"},
		{`rm -f /backups/BACKUP-IS-STALE`, "the marker never clears, so it keeps crying wolf after backups resume and is learned to be ignored"},
		{`set -f`, "pathname expansion is still on: sh expands airbg-*.dump against the container's working directory before find sees it"},
	} {
		if !strings.Contains(command, part.substr) {
			t.Errorf("backup-prune command is missing %q — %s\ngot: %s", part.substr, part.why, command)
		}
	}
}

// ofelia's INI parser strips every double-quote character from a command
// value — established empirically against the pinned image and documented at
// the top of ofelia.ini. The failure that follows is not a parse error: the
// command still runs, with different meaning. `[ -z "$(find ...)" ]` reaches
// sh as `[ -z ]`, a one-argument test that is always true, so a staleness
// alarm written that way fires every night regardless of the backups.
//
// This is a whole-file invariant rather than a per-job one, because the trap
// is that a double-quoted command LOOKS correct in review and in local shell
// testing — the stripping happens only inside ofelia, where nothing is
// watching. The header comment stating the rule is not enough on its own; a
// comment cannot fail a build.
func TestNoOfeliaCommandUsesDoubleQuotes(t *testing.T) {
	const wantJobs = 3 // positive control: a header typo must not silently scan zero jobs
	var checked int
	for _, job := range []string{"collect", "backup", "backup-prune"} {
		command, ok := ofeliaValue(ofeliaJobLines(t, `job-run "`+job+`"`), "command")
		if !ok {
			continue // collect's command is a bare argv, not every job needs sh -c
		}
		checked++
		if strings.Contains(command, `"`) {
			t.Errorf("%s job's command contains a double quote, which ofelia strips before sh sees it — the command will run with different meaning than it reads here\ngot: %s", job, command)
		}
	}
	if checked != wantJobs {
		t.Fatalf("scanned %d job commands, want %d — the job names or the command key changed and this test is checking nothing", checked, wantJobs)
	}
}

// A backslash anywhere in this file is fatal in a way the double-quote trap is
// not: ofelia's INI parser rejects the config before any job is scheduled
// ("unquoted '\' must be followed by new line or double quote"), the daemon
// crash-loops, and collection and backups both stop. The whole file is checked,
// not just command values, because the parser fails on the character wherever
// it appears. Reach for `set -f` instead — see the header of ofelia.ini.
func TestOfeliaConfigContainsNoBackslash(t *testing.T) {
	raw, err := os.ReadFile("ofelia.ini")
	if err != nil {
		t.Fatalf("reading ofelia.ini: %v", err)
	}
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `\`) {
			t.Errorf("ofelia.ini:%d contains a backslash — ofelia will refuse the whole config and run no jobs at all\ngot: %s", n+1, line)
		}
	}
}

// The collect job writes what it fetches, so it has to reach db. It gets one
// network from ofelia (the INI keeps only the last `network =` line), so that
// one network is the only route it has to the database — db must be on it.
func TestCollectJobCanReachTheDatabase(t *testing.T) {
	lines := ofeliaJobLines(t, `job-run "collect"`)
	network, ok := ofeliaValue(lines, "network")
	if !ok {
		t.Fatal("collect job sets no network =")
	}
	name := strings.TrimPrefix(network, "airbg_")
	if _, on := service(t, loadCompose(t), "db").Networks[name]; !on {
		t.Errorf("collect job runs on network %s but db is not attached to %q; the job has exactly one network and cannot reach the database from it", network, name)
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
