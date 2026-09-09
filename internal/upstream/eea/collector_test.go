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
