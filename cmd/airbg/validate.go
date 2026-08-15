package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"airbg.org/internal/config"
)

// runValidateConfig loads the configuration the same way the server does and
// reports it. Exit code, not just output: this is meant to be a deploy gate.
func runValidateConfig(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "listen.addr\t"+cfg.Listen.Addr)
	fmt.Fprintln(w, "listen.metrics_addr\t"+cfg.Listen.MetricsAddr)
	fmt.Fprintln(w, "listen.base_url\t"+cfg.Listen.BaseURL)
	fmt.Fprintf(w, "listen.max_conns\t%d\n", cfg.Listen.MaxConns)
	fmt.Fprintf(w, "database.api_conns\t%d\n", cfg.Database.APIConns)
	fmt.Fprintf(w, "database.collector_conns\t%d\n", cfg.Database.CollectorConns)
	fmt.Fprintf(w, "database.max_inflight\t%d\n", cfg.Database.MaxInflight)
	fmt.Fprintf(w, "upstream.poll_interval\t%v\n", cfg.Upstream.PollInterval)
	fmt.Fprintf(w, "cache.data_max_age\t%v\n", cfg.Cache.DataMaxAge)
	fmt.Fprintf(w, "ratelimit.api\t%v/s burst %v\n", cfg.RateLimit.API.PerSecond, cfg.RateLimit.API.Burst)
	fmt.Fprintf(w, "ratelimit.series\t%v/s burst %v\n", cfg.RateLimit.Series.PerSecond, cfg.RateLimit.Series.Burst)
	fmt.Fprintf(w, "ratelimit.enumerate\t%d areas, %d sensors per %v\n",
		cfg.RateLimit.Enumerate.AreasPerWindow, cfg.RateLimit.Enumerate.SensorsPerWindow, cfg.RateLimit.Enumerate.Window)
	fmt.Fprintf(w, "store.coverage_threshold\t%d\n", cfg.Store.CoverageThreshold)
	// Secrets are reported as present/absent, never printed. A validate command
	// that echoes a connection string is a credential in every CI log that runs
	// it.
	fmt.Fprintf(w, "%s\t%s\n", config.DatabaseURLEnv, presence(cfg.Database.URL))
	w.Flush()
	fmt.Fprintln(stdout, "configuration is valid")
	return 0
}

func presence(v string) string {
	if v == "" {
		return "(not set)"
	}
	return "(set)"
}
