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
// EXEC lets you run commands in the running app container, VOLUMES lets you
// stage a payload, SWARM and SYSTEM reconfigure the daemon itself. IMAGES is
// not among them — see the required list below for why it has to be granted.
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
	// IMAGES is here rather than below because ofelia checks that a job's
	// image exists before creating its container, and does so even with
	// `pull = false`. Denying it 403s every scheduled job, silently.
	for _, required := range []string{"CONTAINERS", "POST", "IMAGES"} {
		if env[required] != "1" {
			t.Errorf("socket-proxy sets %s=%q, want \"1\" — ofelia cannot start jobs without it", required, env[required])
		}
	}
	for _, forbidden := range []string{"EXEC", "NETWORKS", "VOLUMES", "INFO", "SWARM", "SYSTEM"} {
		v, set := env[forbidden]
		if !set || v != "0" {
			t.Errorf("socket-proxy sets %s=%q (present=%v), want an explicit \"0\" — an absent key relies on the image's default instead of the config saying so", forbidden, v, set)
		}
	}
}

// app is internet-facing and stays read_only. socket-proxy cannot be — it
// writes haproxy.cfg at every start — so the exception is pinned here to stop
// the hardening being re-added in good faith. See README.md.
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
func caddyBlocks(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v, want nil", name, err)
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
		t.Fatalf("%s block %q is never closed at column 0", name, current)
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
	blocks := caddyBlocks(t, "Caddyfile")

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

// Caddyfile.dev deliberately drops the enforcement above so a LAN browser can
// reach an airgapped host. That makes it dangerous by design, and the danger is
// only acceptable while it is impossible to install by accident and impossible
// to mistake for the production file. Both properties are asserted here:
// the banner (an operator reading the file on the host sees what it is) and the
// absence of client_auth (if someone "fixes" this file by adding it back, the
// role has two production Caddyfiles and no dev path, which should fail loudly
// rather than silently).
//
// The role installs it only under airbg_open_origin=true; see README.md,
// "the dev Caddyfile".
func TestTheDevCaddyfileIsUnmistakableAndOpen(t *testing.T) {
	data, err := os.ReadFile("Caddyfile.dev")
	if err != nil {
		t.Fatalf("ReadFile(Caddyfile.dev) error = %v, want nil", err)
	}
	if first, _, _ := strings.Cut(string(data), "\n"); !strings.Contains(first, "DEVELOPMENT ONLY") {
		t.Errorf("Caddyfile.dev first line = %q, want it to open with a DEVELOPMENT ONLY banner", first)
	}

	blocks := caddyBlocks(t, "Caddyfile.dev")
	site, ok := blocks["airbg.org"]
	if !ok {
		t.Fatalf("Caddyfile.dev has no airbg.org site block; found %v", keysOf(blocks))
	}
	if strings.Contains(site, "client_auth") {
		t.Error("Caddyfile.dev requires a client certificate, which is the one thing it exists not to do")
	}
	if !strings.Contains(site, "reverse_proxy app:8080") {
		t.Error("Caddyfile.dev does not proxy the app; it would serve nothing")
	}
	if tiles, ok := blocks["tiles.airbg.org"]; !ok {
		t.Errorf("Caddyfile.dev has no tiles.airbg.org site block; the map renders empty without it, found %v", keysOf(blocks))
	} else if !strings.Contains(tiles, "reverse_proxy app:8082") {
		t.Error("Caddyfile.dev tiles block does not proxy the tiles listener")
	}
}

// ofeliaJobLines returns every `key = value` line inside a `[job-run "name"]`
// section of ofelia.ini, in file order, with full-line `;` comments and blank
// lines dropped. Deliberately simple, same spirit as caddyBlocks: sections
// start at column 0 with a `[...]` header and run until the next one. A key
// like `environment` or `volume` can appear more than once in a real section
// — ofelia treats repeats of those as a slice, not a last-wins scalar — so
// every occurrence is returned, not just the last.
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

// Both images are local, so a pull can only fail — and a failed pull fails the
// job silently, since nothing watches a job-run's exit code.
func TestEveryOfeliaJobDisablesTheImagePull(t *testing.T) {
	for _, job := range []string{"backup-prune"} {
		lines := ofeliaJobLines(t, `job-run "`+job+`"`)
		v, ok := ofeliaValue(lines, "pull")
		if !ok || v != "false" {
			t.Errorf("job %s sets pull=%q (present=%v), want \"false\" — the socket proxy forbids image pulls, so this job would 403 on every run and fail silently", job, v, ok)
		}
	}
}

// `network =` is a /networks API call, which the proxy answers 403 to because
// NETWORKS=0. ofelia creates the container regardless, so the job runs on the
// default bridge, resolves no service name, and fails with an error visible
// only in ofelia's own log. The nightly pg_dump did exactly this on every run
// it ever made; it is a host systemd timer now.
//
// Asserted rather than commented because the failure looks like a scheduling
// problem, not a networking one, and `network =` is the obvious thing to reach
// for when a job cannot see the database.
func TestNoOfeliaJobDeclaresANetwork(t *testing.T) {
	for _, job := range []string{"backup-prune"} {
		lines := ofeliaJobLines(t, `job-run "`+job+`"`)
		if v, ok := ofeliaValue(lines, "network"); ok {
			t.Errorf("job %s sets network=%q — the socket proxy sets NETWORKS=0, so the attach is 403'd and the job silently runs on the default bridge. Run it from a host systemd timer instead, as deploy/airbg-backup.service does", job, v)
		}
	}
}

// systemd expands % in a unit, so `date +%Y%m%d` written singly reaches sh as
// whatever the specifier meant and the dump is misnamed. It must be doubled.
func TestBackupUnitEscapesTheDateFormat(t *testing.T) {
	data, err := os.ReadFile("airbg-backup.service")
	if err != nil {
		t.Fatalf("ReadFile(airbg-backup.service) error = %v, want nil", err)
	}
	unit := string(data)
	if !strings.Contains(unit, `+%%Y%%m%%d`) {
		t.Errorf("airbg-backup.service does not contain %s — systemd expands a single %% as a specifier, so the dump would be named after that expansion", `+%%Y%%m%%d`)
	}
	// The dump must land under its final name only once it is complete, or a
	// run killed midway leaves a truncated file that backup-prune counts as a
	// fresh backup and the staleness alarm therefore never fires.
	if !strings.Contains(unit, "/backups/.partial.dump && mv") {
		t.Error("airbg-backup.service does not write .partial.dump and mv it into place; a truncated dump would satisfy the staleness check")
	}
}

// ofelia's job-run only reaps a container when the job finishes, so a command
// that does not return is a container that is never deleted, however plainly
// `delete = true` is written above it. Scheduling one every 5 minutes leaked
// 531 immortal collectors onto the host before anyone looked: the VM sat at
// load 750 with 116 MB free and no swap, and ssh took longer to answer than
// Ansible's 10-second timeout allows, so the deployment that would have fixed
// it could not run either.
//
// `airbg collect` is the command that does not return — it runs its own poll
// loop (cmd/airbg/main.go:83). It also never needed a schedule: `airbg serve`
// polls in-process, and the comment at cmd/airbg/main.go:261 says why a
// separately deployed collector cannot do the job at all, since the snapshot
// the server reads lives in the server's own memory. Every one of those 531
// containers was doing work whose main effect it structurally could not have.
//
// Asserted by command rather than by section name: reintroducing this under
// any other job name would leak exactly the same way.
func TestNoOfeliaJobRunsTheCollector(t *testing.T) {
	data, err := os.ReadFile("ofelia.ini")
	if err != nil {
		t.Fatalf("ReadFile(ofelia.ini) error = %v, want nil", err)
	}
	section := ""
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		v, ok := ofeliaValue([]string{line}, "command")
		if !ok {
			continue
		}
		// Whole-word, so a backup command mentioning the word in a path or a
		// message does not trip this.
		for _, field := range strings.Fields(v) {
			if field == "collect" {
				t.Errorf("%s runs command = %q; `airbg collect` never returns, so ofelia never reaps its container and this leaks one every run. `airbg serve` already polls in-process — see cmd/airbg/main.go:261", section, v)
			}
		}
	}
}

// The backup reaches db over the network its systemd unit names, so that
// network must exist, must carry db, and must be internal: true.
//
// internal is the point. It is tempting to think a job needing the internet
// justifies a non-internal network; it does not. The dump talks only to db,
// and a non-internal network here would hand egress to every service joined to
// it — which is exactly how db acquired internet access once already.
func TestBackupUnitRunsOnAnInternalNetworkCarryingTheDatabase(t *testing.T) {
	data, err := os.ReadFile("airbg-backup.service")
	if err != nil {
		t.Fatalf("ReadFile(airbg-backup.service) error = %v, want nil", err)
	}

	const flag = "--network "
	i := strings.Index(string(data), flag)
	if i < 0 {
		t.Fatal("airbg-backup.service names no --network; docker run would use the default bridge, where the service name db does not resolve")
	}
	network := strings.Fields(string(data)[i+len(flag):])[0]

	c := loadCompose(t)
	name := strings.TrimPrefix(network, "airbg_")
	net, ok := c.Networks[name]
	if !ok {
		t.Fatalf("airbg-backup.service runs on %s, which corresponds to no network in docker-compose.prod.yml (looked for %q)", network, name)
	}
	if !net.Internal {
		t.Errorf("the backup runs on %s, which is not internal: true — the dump talks only to db, so the only thing a non-internal network adds is egress for the services joined to it", network)
	}
	if _, on := service(t, c, "db").Networks[name]; !on {
		t.Errorf("the backup runs on %s but db is not attached to %q, so pg_dump cannot resolve or reach it", network, name)
	}
}

// A failing backup is silent by construction, so the prune job carries the
// alarm. All four parts are asserted: dropping any one restores the silence.
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

// `;` and `#` start an INI comment, silently truncating the command — the
// prune job was registered as `sh -c 'set -f`. Use `&&`/`||` and subshells.
func TestNoOfeliaCommandUsesACommentCharacter(t *testing.T) {
	for _, job := range []string{"backup-prune"} {
		command, ok := ofeliaValue(ofeliaJobLines(t, `job-run "`+job+`"`), "command")
		if !ok {
			continue
		}
		for _, char := range []string{";", "#"} {
			if strings.Contains(command, char) {
				t.Errorf("%s job's command contains %q, which ofelia treats as the start of a comment — everything after it is silently discarded and the job runs truncated\ngot: %s", job, char, command)
			}
		}
	}
}

// ofelia strips every double quote from a command value, so the command runs
// with a different meaning than it reads. Not a parse error — nothing warns.
func TestNoOfeliaCommandUsesDoubleQuotes(t *testing.T) {
	const wantJobs = 1 // positive control: a header typo must not silently scan zero jobs
	var checked int
	for _, job := range []string{"backup-prune"} {
		command, ok := ofeliaValue(ofeliaJobLines(t, `job-run "`+job+`"`), "command")
		if !ok {
			continue // not every job needs a command
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

// A backslash makes ofelia reject the whole config and schedule no job at all.
// The whole file is checked: the parser fails on it wherever it appears.
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

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
