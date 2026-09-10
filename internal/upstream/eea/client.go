package eea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	return &Client{cfg: cfg, http: &http.Client{
		Timeout:       cfg.RequestTimeout,
		CheckRedirect: sameOriginOnly,
	}}
}

// sameOriginOnly refuses a redirect that leaves the scheme+host the request
// started on. net/http follows redirects by default, which would let a 302 in
// an EEA response send the collector's next hop at any host and undo the
// scheme+host allowlist FileURLs applies to the body. via[0] is the original
// request, so a chain that comes back to the first origin is still allowed.
func sameOriginOnly(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	first := via[0].URL
	if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
		return fmt.Errorf("eea: refused redirect from %s://%s to %s://%s",
			first.Scheme, first.Host, req.URL.Scheme, req.URL.Host)
	}
	// net/http's own default; without it a redirect loop spins forever.
	if len(via) >= 10 {
		return fmt.Errorf("eea: stopped after %d redirects", len(via))
	}
	return nil
}

type urlsRequest struct {
	Countries  []string `json:"countries"`
	Cities     []string `json:"cities"`
	Pollutants []string `json:"pollutants"`
	Dataset    int      `json:"dataset"`
	Source     string   `json:"source"`
}

// csvHeader is the first line of the /ParquetFile/urls response. It is not a
// URL, so without skipping it every cycle reports one bogus rejection.
const csvHeader = "ParquetFileUrl"

// utf8BOM prefixes that response. strings.TrimSpace does not treat U+FEFF as
// space, so without stripping it the header line never equals csvHeader.
const utf8BOM = "\ufeff"

// FileURLs returns the parquet file URLs for the configured countries. One
// request covers the whole country list.
//
// The response body is third-party controlled: a candidate line is only kept if
// its host is in EEA.FileHosts and its scheme matches EEA.URL's, so a
// compromised or malicious response cannot steer FetchFile at an arbitrary
// host. The host allowlist is configured rather than derived from EEA.URL
// because the API answers with blob-storage URLs on a different host than its
// own; the scheme still comes from EEA.URL, which validation forces to https.
// Rejections are counted rather than silently dropped.
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

	// A cut list is still a valid list, holding fewer files: truncation drops
	// part of the official layer with no signal, so this is ErrPayloadTooLarge
	// rather than a short read.
	raw, err := readBounded(resp.Body, c.cfg.MaxPayloadBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("eea: file urls: read body: %w", err)
	}

	allowed := make(map[string]bool, len(c.cfg.FileHosts))
	for _, host := range c.cfg.FileHosts {
		allowed[host] = true
	}

	for _, line := range strings.Split(strings.TrimPrefix(string(raw), utf8BOM), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == csvHeader {
			continue
		}
		u, parseErr := url.Parse(line)
		if parseErr != nil || u.Scheme != trusted.Scheme || !allowed[u.Host] {
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
// MaxPayloadBytes must be sized for it: config validation only checks the
// value is > 0 (internal/config/validate.go), so an under-sized bound is
// caught here, at fetch time, as the ErrPayloadTooLarge below.
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

	raw, err := readBounded(resp.Body, c.cfg.MaxPayloadBytes)
	if err != nil {
		return nil, fmt.Errorf("eea: fetch metadata: read body: %w", err)
	}

	md, err := ParseMetadata(bytes.NewReader(raw), c.cfg.Countries)
	if err != nil {
		return nil, err
	}

	if c.cfg.MetadataCache != "" {
		if err := writeCacheFile(metadataCachePath(c.cfg.MetadataCache), raw); err != nil {
			// A failed cache write does not fail the fetch: the caller has a
			// good in-memory copy, only the restart fallback is degraded.
			slog.Warn("eea metadata cache write failed", "error", err)
		}
	}
	return md, nil
}

// ErrPayloadTooLarge reports a body that reached MaxPayloadBytes. It is an
// error rather than a short read because the metadata CSV parses fine when cut
// mid-file: the result is a valid CSV holding fewer stations, which would drop
// the rest of the official layer with no signal at all.
var ErrPayloadTooLarge = errors.New("payload exceeds max_payload_bytes")

// readBounded reads at most max bytes and fails if there were more. Reading
// max+1 is what distinguishes "exactly max bytes of body" from "truncated";
// io.LimitReader alone cannot tell those apart.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("%w (%d bytes)", ErrPayloadTooLarge, max)
	}
	return raw, nil
}

// writeCacheFile replaces path in one step. os.WriteFile truncates the target
// before writing, so a crash or a full disk part-way through a 26 MB write left
// a short file that Collector.loadMetadata's fallback would parse without
// error — a valid CSV holding fewer stations, dropping the rest of the official
// layer silently. The temp file is created in the same directory because
// os.Rename is atomic only within one filesystem.
func writeCacheFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+metadataCacheFile+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// Removes the temp file on every failure path below; a no-op once the
	// rename has moved it away.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; the cache was 0644 before this.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
