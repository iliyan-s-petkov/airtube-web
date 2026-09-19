package snapshot

import (
	"os"
	"testing"
)

// contractJSONPath is where cmd/airbg writes the file this test polices — see
// cmd/airbg/contract.go. Relative from this package because `go test` runs
// with the package directory as its working directory.
const contractJSONPath = "../../web/src/lib/contract.json"

// TestContractJSONIsCurrent is the check that made BBoxQuantumDegrees and
// BBOX_QUANTUM_DEG able to disagree for two commits: a checked-in JSON that no
// longer matches the Go source it was generated from. It runs under plain
// `go test ./...`, so it fires for the Go author before CI ever sees the push.
func TestContractJSONIsCurrent(t *testing.T) {
	c := NewContract()

	// Positive control: without this, a NewContract that silently returned a
	// zero Contract, compared against a contract.json regenerated from that
	// same zero Contract, would be byte-identical and green.
	if len(c.Spans) == 0 || len(c.Spans) != len(FrameSpecs) {
		t.Fatalf("Contract.Spans has %d entries, want %d (len(FrameSpecs))", len(c.Spans), len(FrameSpecs))
	}
	if len(c.Windows) == 0 || len(c.Windows) != len(WindowSpecs) {
		t.Fatalf("Contract.Windows has %d entries, want %d (len(WindowSpecs))", len(c.Windows), len(WindowSpecs))
	}
	if len(c.Hex.TiersKM) == 0 || len(c.Hex.TiersKM) != len(HexTiersKM) {
		t.Fatalf("Contract.Hex.TiersKM has %d entries, want %d (len(HexTiersKM))", len(c.Hex.TiersKM), len(HexTiersKM))
	}

	want, err := c.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := os.ReadFile(contractJSONPath)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./cmd/airbg contract` from the repo root)", contractJSONPath, err)
	}

	if string(got) != string(want) {
		t.Errorf("%s does not match NewContract(); run `go run ./cmd/airbg contract` from the repo root and commit the result\nwant:\n%s\ngot:\n%s",
			contractJSONPath, want, got)
	}
}

// TestContractIsDeterministic kills a map sneaking into Contract: Go
// randomises map iteration per range, so marshalling repeatedly catches a
// map[string]float64 field with probability effectively 1 for any realistic
// map size. Reordering struct fields is not this test's business — every run
// would still reorder the same way — only run-to-run instability is.
func TestContractIsDeterministic(t *testing.T) {
	first, err := NewContract().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := 0; i < 100; i++ {
		got, err := NewContract().Marshal()
		if err != nil {
			t.Fatalf("Marshal (iteration %d): %v", i, err)
		}
		if string(got) != string(first) {
			t.Fatalf("NewContract().Marshal() is not stable across runs (iteration %d); a map field would explain this", i)
		}
	}
}
