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
