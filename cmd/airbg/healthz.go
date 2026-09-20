package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// runHealthz probes the private listener's /healthz endpoint. It is the body
// of the healthz subcommand, split out so it can be driven against an
// httptest server without a real config or process.
func runHealthz(addr string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: got status %d, want 200", resp.StatusCode)
	}
	return nil
}
