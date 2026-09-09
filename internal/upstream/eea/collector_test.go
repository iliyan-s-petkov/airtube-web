package eea_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream/eea"
)

// newStoreForCollector mirrors internal/store/store_test.go's newStore: the
// brief called for testsupport.MigratedPool(t), which does not exist in this
// repo — testsupport.NewPostgres(t) plus db.Migrate is the real helper.
func newStoreForCollector(t *testing.T) (context.Context, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, store.New(pool, testsupport.StoreConfig(), 5*time.Second)
}

func TestRunOnceStoresStationsAndReadings(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(srv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			_, _ = w.Write(metadata)
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer srv.Close()

	ctx, s := newStoreForCollector(t)

	cfg := testConfig(srv.URL, srv.URL+"/metadata.csv")
	cfg.MetadataCache = t.TempDir()
	cfg.MaxPayloadBytes = 64 << 20

	st, err := eea.NewCollector(cfg, s).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if st.Written == 0 {
		t.Fatal("no readings written")
	}

	var n int
	if err := s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM sensor WHERE source = 'eea'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no official stations recorded")
	}

	// Validity <= 0 rows are stored with quality 'source_invalid', not dropped,
	// so a rejected reading is distinguishable from a missing one.
	var invalid int
	if err := s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM reading WHERE quality = 'source_invalid'`).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if invalid == 0 {
		t.Error("no source_invalid rows; the fixture is known to carry Validity = -1 rows")
	}

	// Validity > 0 rows must land with quality 'ok', not just "not
	// source_invalid" — this is the half of the mapping the invalid-count
	// assertion above does not cover.
	var ok int
	if err := s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM reading WHERE quality = 'ok'`).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if ok == 0 {
		t.Error("no ok rows; the fixture is known to carry Validity = 1 rows")
	}

	// bg_station_names.json maps the metadata file's bare national code
	// (which carries no human-readable name) onto the real Bulgarian name for
	// this station's EoI code, BG0070A. See README.md.
	var name string
	if err := s.Pool().QueryRow(ctx,
		`SELECT station_name FROM sensor WHERE source_ref = 'BG/SPO-BG0070A_06001_100'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "София - АИС Копитото" {
		t.Errorf("station_name = %q, want the joined Bulgarian name", name)
	}
}

// The metadata CSV is frozen at 2024-03-11, so a newer sampling point has no
// coordinates. It must be skipped and counted, not guessed at.
func TestRunOnceCountsUnplaceableSamplingPoints(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(srv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			// Header only: parses cleanly, resolves no sampling point. Tab-
			// delimited, matching ParseMetadata's Comma = '\t' (the file is
			// tab-delimited despite its .csv name; see README.md).
			_, _ = w.Write([]byte("Countrycode\tSamplingPoint\tAirQualityStationEoICode\tAirQualityStationNatCode\tLongitude\tLatitude\tAirQualityStationType\tAirQualityStationArea\n"))
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer srv.Close()

	ctx, s := newStoreForCollector(t)

	cfg := testConfig(srv.URL, srv.URL+"/metadata.csv")
	cfg.MetadataCache = t.TempDir()
	cfg.MaxPayloadBytes = 64 << 20

	st, err := eea.NewCollector(cfg, s).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if st.Unplaceable == 0 {
		t.Error("an unplaceable sampling point was not counted")
	}
	if st.Written != 0 {
		t.Errorf("wrote %d readings for a station with no coordinates", st.Written)
	}
}

// A second RunOnce against an unchanged file must take the 304 branch and
// count it, not silently refetch or drop it.
func TestRunOnceCountsUnmodifiedFiles(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(srv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			_, _ = w.Write(metadata)
		default:
			if r.Header.Get("If-Modified-Since") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(parquet)
		}
	}))
	defer srv.Close()

	ctx, s := newStoreForCollector(t)

	cfg := testConfig(srv.URL, srv.URL+"/metadata.csv")
	cfg.MetadataCache = t.TempDir()
	cfg.MaxPayloadBytes = 64 << 20

	c := eea.NewCollector(cfg, s)
	if _, err := c.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	st, err := c.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if st.Unmodified == 0 {
		t.Error("the second pass over an unchanged file was not counted as unmodified")
	}
}

// A second Collector sharing the first's MetadataCache dir must still get
// readings when its own metadata endpoint is down — the on-disk fallback
// written by the first Collector's successful fetch is the only way it can.
func TestRunOnceFallsBackToCachedMetadataAfterRestart(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}

	var goodSrv *httptest.Server
	goodSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(goodSrv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			_, _ = w.Write(metadata)
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer goodSrv.Close()

	ctx, s := newStoreForCollector(t)
	cacheDir := t.TempDir()

	cfg := testConfig(goodSrv.URL, goodSrv.URL+"/metadata.csv")
	cfg.MetadataCache = cacheDir
	cfg.MaxPayloadBytes = 64 << 20

	if _, err := eea.NewCollector(cfg, s).RunOnce(ctx); err != nil {
		t.Fatalf("first collector RunOnce: %v", err)
	}

	// Second collector: file URLs still resolve, but the metadata endpoint is
	// down. It must fall back to the file the first collector's fetch wrote.
	var brokenSrv *httptest.Server
	brokenSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(brokenSrv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer brokenSrv.Close()

	cfg2 := testConfig(brokenSrv.URL, brokenSrv.URL+"/metadata.csv")
	cfg2.MetadataCache = cacheDir
	cfg2.MaxPayloadBytes = 64 << 20

	st, err := eea.NewCollector(cfg2, s).RunOnce(ctx)
	if err != nil {
		t.Fatalf("second collector RunOnce: %v", err)
	}
	if st.Written == 0 {
		t.Error("no readings written; the metadata disk-cache fallback did not kick in")
	}
}
