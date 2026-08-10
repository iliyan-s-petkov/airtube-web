package httpx

// Cloudflare's published proxy ranges, from https://www.cloudflare.com/ips/
// (retrieved 2026-08-09).
//
// Embedded rather than fetched at startup on purpose. Fetching would make the
// binary's security posture depend on a network call succeeding at boot: a
// failed fetch either fails closed (the service will not start) or fails open
// (nothing is trusted, so every request is attributed to Cloudflare's own
// address and one bucket rate-limits the entire internet). Neither is
// acceptable, and Cloudflare changes these ranges rarely.
//
// AIRBG_TRUSTED_PROXY_CIDRS overrides this list, so a range change is a config
// edit and a restart, not a rebuild.
func DefaultCloudflareCIDRs() []string {
	return []string{
		// IPv4
		"173.245.48.0/20",
		"103.21.244.0/22",
		"103.22.200.0/22",
		"103.31.4.0/22",
		"141.101.64.0/18",
		"108.162.192.0/18",
		"190.93.240.0/20",
		"188.114.96.0/20",
		"197.234.240.0/22",
		"198.41.128.0/17",
		"162.158.0.0/15",
		"104.16.0.0/13",
		"104.24.0.0/14",
		"172.64.0.0/13",
		"131.0.72.0/22",
		// IPv6
		"2400:cb00::/32",
		"2606:4700::/32",
		"2803:f800::/32",
		"2405:b500::/32",
		"2405:8100::/32",
		"2a06:98c0::/29",
		"2c0f:f248::/32",
	}
}
