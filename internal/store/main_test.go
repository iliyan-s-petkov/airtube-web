package store_test

import (
	"fmt"
	"os"
	"testing"

	"airbg.org/internal/testsupport"
)

// One container for the whole package instead of one per test. At 66 tests the
// per-test idiom was starting 66 containers per run, and Docker intermittently
// failed to publish 5432/tcp under that churn — a "port 5432/tcp not found" at
// container start, in whichever test was unlucky, which failed the package.
// Each test still gets its own empty database and migrates it itself.
func TestMain(m *testing.M) {
	stop, err := testsupport.StartSharedPostgres()
	if err != nil {
		fmt.Fprintf(os.Stderr, "shared postgres: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	stop() // before os.Exit, which skips defers
	os.Exit(code)
}
