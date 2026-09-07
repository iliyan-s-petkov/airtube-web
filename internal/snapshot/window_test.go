package snapshot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/snapshot"
)

// seedWindowed gives the three seeded sensors a rollup history whose mean is
// nothing like their current reading, so any body that still carries the live
// number is obvious rather than plausible.
func seedWindowed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) {
	t.Helper()
	for i := 0; i < 3; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
			 VALUES ($1, $2, 'P2', $3, $3, $3, 6)`,
			now.Add(-time.Hour).Truncate(time.Hour), int64(100+i), float64(i+1))
		if err != nil {
			t.Fatalf("seed reading_hourly: %v", err)
		}
	}
}

func areaValues(t *testing.T, body snapshot.Body, slug string) map[string]float64 {
	t.Helper()
	var p struct {
		Areas []struct {
			Slug   string             `json:"slug"`
			Values map[string]float64 `json:"values"`
		} `json:"areas"`
	}
	if err := json.Unmarshal(body.JSON, &p); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	for _, a := range p.Areas {
		if a.Slug == slug {
			return a.Values
		}
	}
	t.Fatalf("area %q missing from overview", slug)
	return nil
}

func TestWindowServesTheWindowedAverageNotTheLiveReading(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)
	now := time.Now().UTC().Truncate(time.Minute)
	seedWindowed(t, ctx, pool, now)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := areaValues(t, snap.Overview, "sofia")["P2"]; got < 19.9 || got > 20.1 {
		t.Errorf("live P2 = %v, want 20 (mean of the latest 10, 20, 30)", got)
	}
	if got := areaValues(t, snap.Window("24h").Overview, "sofia")["P2"]; got < 1.9 || got > 2.1 {
		t.Errorf("24h P2 = %v, want 2 (mean of the hourly 1, 2, 3)", got)
	}
}

// Every window in the published list has to be built, or the selector offers an
// option that quietly answers with live data.
func TestWindowBuildsEveryPublishedWindow(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)
	now := time.Now().UTC().Truncate(time.Minute)
	seedWindowed(t, ctx, pool, now)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, spec := range snapshot.WindowSpecs {
		w := snap.Window(spec.Name)
		if w == snap {
			t.Errorf("window %q returned the live snapshot", spec.Name)
			continue
		}
		if bytes.Equal(w.Overview.JSON, snap.Overview.JSON) {
			t.Errorf("window %q overview is byte-identical to live", spec.Name)
		}
		if w.Overview.ETag == snap.Overview.ETag {
			t.Errorf("window %q overview ETag equals live; a cache would serve one for the other", spec.Name)
		}
	}
}

// An unknown name is the handler's problem, not this method's: returning the
// live view keeps every caller on a real snapshot, and the 400 is raised where
// there is a response to write it to.
func TestWindowFallsBackToLiveForAnUnknownName(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)
	now := time.Now().UTC().Truncate(time.Minute)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Window("nonsense") != snap || snap.Window("") != snap {
		t.Error("Window did not return the live snapshot for an unknown or empty name")
	}
	// A windowed view has no windows of its own: asking one for a window must
	// terminate rather than hand back a third thing.
	w := snap.Window("24h")
	if w.Window("7d") != w {
		t.Error("Window on a windowed snapshot did not return itself")
	}
}

// The window changes the numbers and nothing else. Boundaries, wind and the
// known-slug set describe the country and the cycle, not the reader's choice.
func TestWindowSharesTheCountryWideBodies(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)
	now := time.Now().UTC().Truncate(time.Minute)
	seedWindowed(t, ctx, pool, now)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	w := snap.Window("7d")
	if w.Boundaries.ETag != snap.Boundaries.ETag {
		t.Error("windowed boundaries differ from live; the outlines do not depend on the window")
	}
	if len(w.KnownSlugs) != len(snap.KnownSlugs) {
		t.Errorf("windowed slugs = %d, live = %d; a window must not invent or lose an area",
			len(w.KnownSlugs), len(snap.KnownSlugs))
	}
	for slug := range snap.AreaSensors {
		if _, ok := w.AreaSensors[slug]; !ok {
			t.Errorf("windowed AreaSensors is missing %q; a missing key means 404", slug)
		}
	}
}

// The area detail page draws its markers from AreaSensors, so the window has to
// reach that payload too — otherwise the province map keeps showing live values
// under a selector that says "last week".
func TestWindowAveragesTheAreaSensorPayload(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)
	now := time.Now().UTC().Truncate(time.Minute)
	seedWindowed(t, ctx, pool, now)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	values := func(b snapshot.Body) []*float64 {
		var p struct {
			Sensors struct {
				P2 []*float64 `json:"P2"`
			} `json:"sensors"`
		}
		if err := json.Unmarshal(b.JSON, &p); err != nil {
			t.Fatalf("unmarshal sensors: %v", err)
		}
		return p.Sensors.P2
	}
	live, windowed := values(snap.AreaSensors["sofia"]), values(snap.Window("24h").AreaSensors["sofia"])
	if len(live) != 3 || len(windowed) != 3 {
		t.Fatalf("P2 columns: live %d, windowed %d, want 3 each", len(live), len(windowed))
	}
	for i := range live {
		if live[i] == nil || windowed[i] == nil {
			t.Fatalf("sensor %d has a nil value in one of the views", i)
		}
		if *live[i] == *windowed[i] {
			t.Errorf("sensor %d: windowed value %v equals the live one", i, *live[i])
		}
	}
}
