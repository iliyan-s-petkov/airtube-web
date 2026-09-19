package store

import "testing"

// TestWriteBatchRanges is the killing test for the writeBatchLimit mutation
// survivor: it pins the exact chunk boundaries writeBatchRanges produces
// against the real writeBatchLimit, so mutating the constant breaks this
// test directly, with no need to instrument WriteReadings or export a hook
// onto the production write path.
func TestWriteBatchRanges(t *testing.T) {
	// Literal totals, not derived from writeBatchLimit: if that changed, a
	// count expected relative to it would silently keep matching under a
	// mutation. These two are the cases the brief names.

	// Exactly 1000 rows: one flush, not two, against the real limit.
	got := writeBatchRanges(1000, writeBatchLimit)
	want := [][2]int{{0, 1000}}
	if !rangesEqual(got, want) {
		t.Errorf("writeBatchRanges(1000, %d) = %v, want %v (%d flush(es), want 1)", writeBatchLimit, got, want, len(got))
	}

	// 1001 rows: must not silently round up into a single oversized flush.
	got = writeBatchRanges(1001, writeBatchLimit)
	want = [][2]int{{0, 1000}, {1000, 1001}}
	if !rangesEqual(got, want) {
		t.Errorf("writeBatchRanges(1001, %d) = %v, want %v (%d flush(es), want 2)", writeBatchLimit, got, want, len(got))
	}
}

func rangesEqual(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
