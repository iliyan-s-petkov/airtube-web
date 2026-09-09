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
