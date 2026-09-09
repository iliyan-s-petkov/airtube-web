package eea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"airbg.org/internal/config"
)

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
func (c *Client) FileURLs(ctx context.Context) ([]string, error) {
	body, err := json.Marshal(urlsRequest{
		Countries:  c.cfg.Countries,
		Cities:     []string{},
		Pollutants: []string{},
		Dataset:    datasetUTD,
		Source:     "API",
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.URL, "/")+"/ParquetFile/urls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eea: file urls: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eea: file urls: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("eea: file urls: read body: %w", err)
	}

	var urls []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http") {
			urls = append(urls, line)
		}
	}
	return urls, nil
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
	return ParseMetadata(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes), c.cfg.Countries)
}
