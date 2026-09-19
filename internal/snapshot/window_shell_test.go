package snapshot

import "testing"

// windowShell is the database-free half of buildWindow: the live snapshot
// copied, with the fields a window must not inherit reset. buildWindow itself
// needs Postgres and is exercised in window_test.go (package snapshot_test);
// these tests cover the part that does not.

// The whole point of windowShell: a window must get its own cache, not a
// pointer to the live snapshot's. Otherwise a windowed request could hit a
// key the live snapshot already populated and be served live data.
func TestWindowShellGetsItsOwnBodyCache(t *testing.T) {
	live := pointFixture()
	shell := live.windowShell()
	if shell.bodies == live.bodies {
		t.Fatal("windowShell shares the live snapshot's body cache")
	}
}

// This is the test that matters. A body memoised on the live snapshot must
// not be served, under the same key, by a window built from it — that would
// answer a windowed request with a live reading. Asserted on identity, not
// equality, the way TestClippedBodyIsMemoisedPerSnapshot does: two distinct
// encodes of the same fixture can be byte-equal, so only pointer identity
// tells "served from the live cache" apart from "encoded fresh".
func TestWindowShellDoesNotServeTheLiveSnapshotsCachedBody(t *testing.T) {
	live := pointFixture()
	bb := BBox{W: 23.0, S: 42.0, E: 24.0, N: 43.0}

	liveBody, err := live.PointBody(bb)
	if err != nil {
		t.Fatalf("PointBody on live: %v", err)
	}

	shell := live.windowShell()
	shellBody, err := shell.PointBody(bb)
	if err != nil {
		t.Fatalf("PointBody on shell: %v", err)
	}

	if len(liveBody.Gzip) == 0 || len(shellBody.Gzip) == 0 {
		t.Fatal("a body came back without gzip bytes")
	}
	if &liveBody.Gzip[0] == &shellBody.Gzip[0] {
		t.Error("the shell served the live snapshot's cached body under the same key")
	}
}

// Windows is reset to nil, so Window() on a window is the identity and cannot
// recurse into a window of a window.
func TestWindowShellClearsWindows(t *testing.T) {
	live := pointFixture()
	live.Windows = map[string]*Snapshot{"7d": pointFixture()}

	shell := live.windowShell()
	if shell.Windows != nil {
		t.Errorf("windowShell().Windows = %v, want nil", shell.Windows)
	}
	if got := shell.Window("7d"); got != shell {
		t.Error("Window() on a shell did not return itself")
	}
}
