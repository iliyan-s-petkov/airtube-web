package snapshot_test

import (
	"sync"
	"testing"
	"time"

	"airbg.org/internal/snapshot"
)

// TestHolderReturnsNilBeforeFirstStore pins the 503 precondition. A holder that
// returned an empty &Snapshot{} instead of nil would let handlers serve an empty
// country as though it had been measured — the "reports success while storing
// nothing" failure this project keeps guarding against.
func TestHolderReturnsNilBeforeFirstStore(t *testing.T) {
	h := snapshot.NewHolder()
	if got := h.Load(); got != nil {
		t.Fatalf("Load() = %+v before any Store, want nil", got)
	}
}

// TestHolderIsRaceFree is run under -race. Concurrent readers during a publish
// is the actual production pattern: the ingest goroutine stores while every
// in-flight request loads.
func TestHolderIsRaceFree(t *testing.T) {
	h := snapshot.NewHolder()
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
