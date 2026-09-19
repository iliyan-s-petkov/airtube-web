package snapshot_test

import (
	"sync"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/snapshot"
)

// TestNewHolderTakesDefaultMetricFromConfig proves NewHolder actually consumes
// cfg.DefaultMetric rather than a package literal that happens to agree with
// airbg.yaml's "P2". A holder built from a config naming a different metric
// must report that metric back, not "P2".
func TestNewHolderTakesDefaultMetricFromConfig(t *testing.T) {
	h := snapshot.NewHolder(config.Series{DefaultMetric: "temperature", DefaultWindow: time.Hour}, config.Wind{})
	if got := h.DefaultMetric(); got != "temperature" {
		t.Errorf("DefaultMetric() = %q, want %q", got, "temperature")
	}
}

// TestHolderReturnsNilBeforeFirstStore pins the 503 precondition. A holder that
// returned an empty &Snapshot{} instead of nil would let handlers serve an empty
// country as though it had been measured — the "reports success while storing
// nothing" failure this project keeps guarding against.
func TestHolderReturnsNilBeforeFirstStore(t *testing.T) {
	h := testHolder(t)
	if got := h.Load(); got != nil {
		t.Fatalf("Load() = %+v before any Store, want nil", got)
	}
}

// TestHolderIsRaceFree is run under -race. Concurrent readers during a publish
// is the actual production pattern: the ingest goroutine stores while every
// in-flight request loads.
func TestHolderIsRaceFree(t *testing.T) {
	h := testHolder(t)
	h.Store(&snapshot.Snapshot{GeneratedAt: time.Unix(1, 0)})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if s := h.Load(); s == nil {
					t.Error("Load() returned nil after a Store")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 500; j++ {
			h.Store(&snapshot.Snapshot{GeneratedAt: time.Unix(int64(j+2), 0)})
		}
	}()
	wg.Wait()
}

// TestNewHolderRespectesWindArgument proves NewHolder actually uses the wind
// argument passed to it. The constructor argument must be load-bearing, not
// ignored. This test fails if NewHolder ignores the wind argument and always
// uses config.Wind{}.
func TestNewHolderRespectesWindArgument(t *testing.T) {
	series := config.Series{DefaultMetric: "P2", DefaultWindow: time.Hour}

	// Holder with wind disabled
	disabledWind := snapshot.NewHolder(series, config.Wind{Enabled: false})
	if got := disabledWind.WindForTesting(); got.Enabled {
		t.Errorf("NewHolder with Enabled=false resulted in wind.Enabled=%v, want false", got.Enabled)
	}

	// Holder with wind enabled and a specific resolution
	enabledConfig := config.Wind{
		Enabled:       true,
		ResolutionDeg: 0.25,
	}
	enabledWind := snapshot.NewHolder(series, enabledConfig)
	if got := enabledWind.WindForTesting(); !got.Enabled || got.ResolutionDeg != 0.25 {
		t.Errorf("NewHolder with wind enabled resulted in %+v, want %+v", got, enabledConfig)
	}
}
