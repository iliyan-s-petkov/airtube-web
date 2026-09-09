package eea_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ParquetFile/urls" {
			t.Errorf("got %s %s, want POST /ParquetFile/urls", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("https://example.invalid/a.parquet\nhttps://example.invalid/b.parquet\n"))
	}))
	defer srv.Close()

	urls, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2", len(urls))
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

	if _, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background()); err == nil {
		t.Error("FileURLs accepted a 502")
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

// The header truncated below its required columns is the observable proof
// that FetchMetadata stops reading at MaxPayloadBytes rather than at EOF.
func TestFetchMetadataBoundsTheBody(t *testing.T) {
	header := "Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"
	row := "BG\tSP1\tCODE1\tNAT1\t23.32\t42.69\tbackground\turban\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(header + row))
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = 10 // shorter than the header row itself
	if _, err := eea.New(cfg).FetchMetadata(context.Background()); err == nil {
		t.Error("FetchMetadata succeeded on a header cut mid-row by MaxPayloadBytes; want a parse error")
	}
}

// The second URL is cut clean at the line boundary, so its absence from the
// result is the observable proof of the bound rather than of the line filter.
func TestFileURLsBoundsTheBody(t *testing.T) {
	line1 := "https://example.invalid/a.parquet\n"
	line2 := "https://example.invalid/b.parquet\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(line1 + line2))
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = int64(len(line1))
	urls, err := eea.New(cfg).FileURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 {
		t.Errorf("got %d urls, want 1 — the second line should have been cut by MaxPayloadBytes", len(urls))
	}
}
