package eea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"airbg.org/internal/config"
)

// metadataCacheFile is the on-disk name for the cached coordinate CSV,
// shared by the write side (FetchMetadata) and the read fallback
// (Collector.loadMetadata).
const metadataCacheFile = "PanEuropean_metadata.csv"

func metadataCachePath(dir string) string {
	return filepath.Join(dir, metadataCacheFile)
}

const userAgent = "airbg.org collector (+https://airbg.org)"

// datasetUTD is the near-real-time set, ~1h behind. Datasets 2 and 3 are the
// verified archives and lag by years.
const datasetUTD = 1

type Client struct {
	cfg  config.EEA
	http *http.Client
}

func New(cfg config.EEA) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.RequestTimeout}}
}

type urlsRequest struct {
	Countries  []string `json:"countries"`
	Cities     []string `json:"cities"`
	Pollutants []string `json:"pollutants"`
	Dataset    int      `json:"dataset"`
	Source     string   `json:"source"`
}

// FileURLs returns the parquet file URLs for the configured countries. One
// request covers the whole country list.
//
// The response body is third-party controlled: a candidate line is only kept
// if its scheme and host match the configured EEA.URL, so a compromised or
// malicious response cannot steer FetchFile at an arbitrary host. Rejections
// are counted rather than silently dropped.
func (c *Client) FileURLs(ctx context.Context) (urls []string, rejected int, err error) {
	trusted, err := url.Parse(c.cfg.URL)
	if err != nil {
		return nil, 0, fmt.Errorf("eea: file urls: configured URL: %w", err)
	}

	body, err := json.Marshal(urlsRequest{
		Countries:  c.cfg.Countries,
		Cities:     []string{},
		Pollutants: []string{},
		Dataset:    datasetUTD,
		Source:     "API",
	})
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.URL, "/")+"/ParquetFile/urls", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("eea: file urls: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("eea: file urls: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("eea: file urls: read body: %w", err)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		u, parseErr := url.Parse(line)
		if parseErr != nil || u.Scheme != trusted.Scheme || u.Host != trusted.Host {
			rejected++
			continue
		}
		urls = append(urls, line)
	}
	return urls, rejected, nil
}

// FetchFile downloads one Parquet file. modified is false on a 304, where the
// body is empty and the caller keeps what it already stored.
func (c *Client) FetchFile(ctx context.Context, url string, since time.Time) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	if !since.IsZero() {
		req.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("eea: fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("eea: fetch file: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, false, fmt.Errorf("eea: fetch file: read body: %w", err)
	}
	return body, true, nil
}

// FetchMetadata downloads and parses the coordinate CSV. It is 26 MB, so
// MaxPayloadBytes must be sized for it — see the validate rule in
// internal/config/validate.go.
func (c *Client) FetchMetadata(ctx context.Context) (Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.MetadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eea: fetch metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eea: fetch metadata: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("eea: fetch metadata: read body: %w", err)
	}

	md, err := ParseMetadata(bytes.NewReader(raw), c.cfg.Countries)
	if err != nil {
		return nil, err
	}

	if c.cfg.MetadataCache != "" {
		if err := os.WriteFile(metadataCachePath(c.cfg.MetadataCache), raw, 0o644); err != nil {
			// A failed cache write does not fail the fetch: the caller has a
			// good in-memory copy, only the restart fallback is degraded.
			slog.Warn("eea metadata cache write failed", "error", err)
		}
	}
	return md, nil
}
