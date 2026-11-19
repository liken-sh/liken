package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testTimeout limits a test that runs the stub pw-dump, a real process
// outside a synctest bubble, so a pw-dump that hangs fails the test in
// seconds.
const testTimeout = 5 * time.Second

// stubProbeDump puts a pw-dump on PATH that prints a graph larger than
// the 10 KiB of probe output that the kubelet keeps, writes a line to
// stderr, and exits with the given status.
func stubProbeDump(t *testing.T, exitStatus string) {
	t.Helper()
	dir := t.TempDir()
	graph := filepath.Join(dir, "graph.json")
	if err := os.WriteFile(graph, bytes.Repeat([]byte(`{"id": 1},`), 8192), 0o644); err != nil {
		t.Fatalf("writing the stub graph: %v", err)
	}
	script := "#!/bin/sh\ncat " + graph + "\necho 'remote error: Connection refused' >&2\nexit " + exitStatus + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the stub pw-dump: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
