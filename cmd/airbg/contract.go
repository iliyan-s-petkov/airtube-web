package main

import (
	"fmt"
	"io"
	"os"

	"airbg.org/internal/snapshot"
)

// defaultContractPath is relative to the process's working directory, which is
// the repo root for every documented dev and CI command.
const defaultContractPath = "web/src/lib/contract.json"

// runContract writes the generated frontend contract. It needs no config, no
// database and no network, so it can run in a CI job that has neither.
//
// Writes to a path rather than stdout-with-redirection: `go run ./cmd/airbg
// contract > path` truncates the target before the program runs, so a failure
// — a compile error upstream, a panic — would leave a zero-byte file and a git
// diff about data the tool meant to maintain having been destroyed.
//
// Merge conflicts in the generated file resolve mechanically: fix the Go
// conflict, then re-run this command.
func runContract(args []string, stdout, stderr io.Writer) int {
	path := defaultContractPath
	if len(args) > 0 {
		path = args[0]
	}

	b, err := snapshot.NewContract().Marshal()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}
