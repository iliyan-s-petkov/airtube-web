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
