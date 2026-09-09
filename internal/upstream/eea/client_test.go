package eea_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/upstream/eea"
)

func testConfig(base, metadata string) config.EEA {
	return config.EEA{
		Enabled:         true,
		URL:             base,
		MetadataURL:     metadata,
		Countries:       []string{"BG"},
		RequestTimeout:  5 * time.Second,
		PollInterval:    time.Hour,
		MinPollInterval: 15 * time.Minute,
		MaxPayloadBytes: 1 << 20,
	}
}

func TestFileURLsPostsTheDocumentedBody(t *testing.T) {
	var got struct {
		Countries  []string `json:"countries"`
		Pollutants []string `json:"pollutants"`
		Dataset    int      `json:"dataset"`
		Source     string   `json:"source"`
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ParquetFile/urls" {
			t.Errorf("got %s %s, want POST /ParquetFile/urls", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(srv.URL + "/a.parquet\n" + srv.URL + "/b.parquet\n"))
	}))
	defer srv.Close()

	urls, rejected, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2", len(urls))
	}
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0 for same-origin urls", rejected)
	}
	if len(got.Countries) != 1 || got.Countries[0] != "BG" {
		t.Errorf("countries = %v, want [BG]", got.Countries)
	}
	// dataset 1 is UTD, the near-real-time set. 2 and 3 are the verified
	// archives, which are years behind.
	if got.Dataset != 1 {
		t.Errorf("dataset = %d, want 1", got.Dataset)
	}
}

func TestFetchFileReportsNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Modified-Since") == "" {
			t.Error("no If-Modified-Since header; every unchanged file would be refetched")
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	body, modified, err := eea.New(testConfig(srv.URL, srv.URL)).
		FetchFile(context.Background(), srv.URL+"/a.parquet", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("a 304 was reported as modified")
	}
	if len(body) != 0 {
		t.Errorf("a 304 returned %d bytes of body", len(body))
	}
}

// The bound is the whole defence against a hostile or broken response: without
// it one oversized file is an out-of-memory kill of the whole server process.
func TestFetchFileBoundsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = 1024
	body, modified, err := eea.New(cfg).FetchFile(context.Background(), srv.URL+"/a.parquet", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("a 200 was reported as not modified")
	}
	if len(body) > 1024 {
		t.Errorf("read %d bytes, want at most 1024", len(body))
	}
}

func TestFileURLsRejectsANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, _, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background()); err == nil {
		t.Error("FileURLs accepted a 502")
	}
}

// A malicious or compromised /ParquetFile/urls response naming an off-host
// URL must be refused, not followed — FetchFile would otherwise download
// from wherever the response body points.
func TestFileURLsRejectsOffHostURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("https://attacker.invalid/a.parquet\n"))
	}))
	defer srv.Close()

	urls, rejected, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 0 {
		t.Errorf("got %d urls, want 0 — the off-host url should have been refused", len(urls))
	}
	if rejected != 1 {
		t.Errorf("rejected = %d, want 1", rejected)
	}
}

// A server that never replies must not hang the collector forever: the
// timeout is the only thing that turns a stuck upstream into an error.
func TestFetchFileRespectsRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	cfg.RequestTimeout = 20 * time.Millisecond
	if _, _, err := eea.New(cfg).FetchFile(context.Background(), srv.URL+"/a.parquet", time.Time{}); err == nil {
		t.Error("FetchFile succeeded past RequestTimeout; want a timeout error")
	}
}

const (
	mdHeader = "Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"
	mdRow    = "BG\tSP1\tCODE1\tNAT1\t23.32\t42.69\tbackground\turban\n"
)

func metadataServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mdHeader + mdRow))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A body over MaxPayloadBytes must fail rather than parse short: a CSV cut
// mid-file is still valid CSV, so a short read would drop the remaining
// stations from the official layer with no signal at all.
func TestFetchMetadataRejectsAnOversizedBody(t *testing.T) {
	srv := metadataServer(t)
	cfg := testConfig(srv.URL, srv.URL)
	// One byte short of the body, so only the bound can reject it — the CSV
	// itself parses.
	cfg.MaxPayloadBytes = int64(len(mdHeader+mdRow)) - 1
	_, err := eea.New(cfg).FetchMetadata(context.Background())
	if !errors.Is(err, eea.ErrPayloadTooLarge) {
		t.Errorf("got %v, want ErrPayloadTooLarge", err)
	}
}

// A body of exactly MaxPayloadBytes is within the bound; off by one here
// would reject every response whose length lands on the limit.
func TestFetchMetadataAcceptsABodyExactlyAtTheLimit(t *testing.T) {
	srv := metadataServer(t)
	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = int64(len(mdHeader + mdRow))
	md, err := eea.New(cfg).FetchMetadata(context.Background())
	if err != nil {
		t.Fatalf("a body of exactly MaxPayloadBytes was rejected: %v", err)
	}
	if len(md) != 1 {
		t.Errorf("got %d stations, want 1", len(md))
	}
}

// Setting CheckRedirect replaces net/http's own 10-hop cap, so a same-host
// redirect loop would otherwise spin until RequestTimeout on every poll.
func TestClientStopsASameHostRedirectLoop(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	// Long enough that the timeout cannot be what ends the loop; the hop count
	// below is the assertion, since any stop produces an error.
	cfg.RequestTimeout = 30 * time.Second
	if _, _, err := eea.New(cfg).FetchFile(context.Background(), srv.URL+"/loop", time.Time{}); err == nil {
		t.Error("FetchFile followed a redirect loop without stopping")
	}
	if n := hits.Load(); n > 11 {
		t.Errorf("the client made %d hops round a redirect loop, want at most 11", n)
	}
}

// A 302 to another host must not be followed: the scheme+host allowlist
// FileURLs applies to the response body is worthless if net/http will chase a
// redirect off it.
func TestClientRefusesACrossHostRedirect(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the client followed a redirect to another host")
		_, _ = w.Write([]byte(mdHeader + mdRow))
	}))
	defer elsewhere.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	if _, _, err := eea.New(cfg).FetchFile(context.Background(), srv.URL+"/a.parquet", time.Time{}); err == nil {
		t.Error("FetchFile followed a cross-host redirect; want an error")
	}
	if _, err := eea.New(cfg).FetchMetadata(context.Background()); err == nil {
		t.Error("FetchMetadata followed a cross-host redirect; want an error")
	}
}

// Same-host redirects stay allowed: the EEA server moves paths around, and
// refusing those breaks the fetch for no security gain.
func TestClientFollowsASameHostRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a.parquet" {
			http.Redirect(w, r, "/moved.parquet", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("PAR1"))
	}))
	defer srv.Close()

	body, _, err := eea.New(testConfig(srv.URL, srv.URL)).
		FetchFile(context.Background(), srv.URL+"/a.parquet", time.Time{})
	if err != nil {
		t.Fatalf("a same-host redirect was refused: %v", err)
	}
	if string(body) != "PAR1" {
		t.Errorf("got %q, want the redirected body", body)
	}
}

// A successful fetch must persist the raw CSV to MetadataCache so a restart
// can fall back to it — this is the write side of that fallback.
func TestFetchMetadataWritesTheCacheFile(t *testing.T) {
	header := "Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"
	row := "BG\tSP1\tCODE1\tNAT1\t23.32\t42.69\tbackground\turban\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(header + row))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := testConfig(srv.URL, srv.URL)
	cfg.MetadataCache = dir
	if _, err := eea.New(cfg).FetchMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "PanEuropean_metadata.csv"))
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if string(got) != header+row {
		t.Errorf("cache file = %q, want %q", got, header+row)
	}

	// os.CreateTemp makes the file 0600, so the atomic write has to restore
	// the 0644 the cache had when os.WriteFile made it.
	info, err := os.Stat(filepath.Join(dir, "PanEuropean_metadata.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("cache file mode = %v, want -rw-r--r--", info.Mode().Perm())
	}

	// The write goes through a temp file in the same directory; leaving one
	// behind would add 26 MB to the cache directory every cycle.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache dir holds %v, want only the cache file", names)
	}
}

// The cache write is atomic: the reader is Collector.loadMetadata's restart
// fallback, and a half-written CSV parses clean as a shorter station list, so a
// failed write must leave the previous copy exactly as it was rather than
// truncate it. Proved with a read-only directory, which stops a new file being
// created but not an existing one being opened for writing — the difference
// between os.Rename and os.WriteFile.
func TestFetchMetadataDoesNotTruncateTheCacheWhenTheWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	header := "Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"
	row := "BG\tSP1\tCODE1\tNAT1\t23.32\t42.69\tbackground\turban\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(header + row))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "PanEuropean_metadata.csv")
	const previous = "the previous cycle's copy\n"
	if err := os.WriteFile(path, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MetadataCache = dir
	if _, err := eea.New(cfg).FetchMetadata(context.Background()); err != nil {
		t.Fatalf("a failed cache write must not fail the fetch: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != previous {
		t.Errorf("cache file = %q, want the previous copy %q untouched", got, previous)
	}
}

// The failure path has to clean up after itself too. A directory where the
// cache file belongs lets the temp file be created and written and makes only
// the rename fail, which is the one window in which a temp file can be
// stranded; a cycle-per-hour collector would otherwise fill the disk with them.
func TestFetchMetadataRemovesItsTempFileWhenTheRenameFails(t *testing.T) {
	header := "Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"
	row := "BG\tSP1\tCODE1\tNAT1\t23.32\t42.69\tbackground\turban\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(header + row))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "PanEuropean_metadata.csv"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MetadataCache = dir
	if _, err := eea.New(cfg).FetchMetadata(context.Background()); err != nil {
		t.Fatalf("a failed cache write must not fail the fetch: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache dir holds %v, want no temp file left behind", names)
	}
}

// The second URL is cut clean at the line boundary, so its absence from the
// result is the observable proof of the bound rather than of the line filter.
func TestFileURLsBoundsTheBody(t *testing.T) {
	var srv *httptest.Server
	var line1, line2 string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(line1 + line2))
	}))
	defer srv.Close()
	line1 = srv.URL + "/a.parquet\n"
	line2 = srv.URL + "/b.parquet\n"

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = int64(len(line1))
	urls, _, err := eea.New(cfg).FileURLs(context.Background())
	if !errors.Is(err, eea.ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge: a cut list parses as a shorter list, so a short read would drop files silently", err)
	}
	if urls != nil {
		t.Errorf("got %d urls alongside the error, want none", len(urls))
	}
}
