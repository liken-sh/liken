package main

// The PipeWire container's startup probe, against a pw-dump stand-in
// on the path.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubProbeDump puts a pw-dump on the path that prints a graph larger
// than the kubelet keeps of a probe's output, writes a line to
// stderr, and exits with the given status.
func stubProbeDump(t *testing.T, exitStatus string) {
	t.Helper()
	dir := t.TempDir()
	graph := filepath.Join(dir, "graph.json")
	if err := os.WriteFile(graph, bytes.Repeat([]byte(`{"id": 1},`), 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + graph + "\necho 'remote error: Connection refused' >&2\nexit " + exitStatus + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGraphAnswersPassesWhenPWDumpSucceeds(t *testing.T) {
	stubProbeDump(t, "0")
	if err := graphAnswers(context.Background()); err != nil {
		t.Fatalf("a pw-dump that exits 0 passes the probe: %v", err)
	}
}

func TestGraphAnswersReportsTheFailureWithoutTheGraph(t *testing.T) {
	stubProbeDump(t, "3")
	err := graphAnswers(context.Background())
	if err == nil {
		t.Fatal("a pw-dump that exits nonzero fails the probe")
	}
	want := "running pw-dump: exit status 3: remote error: Connection refused"
	if err.Error() != want {
		t.Errorf("the probe's report = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), `"id"`) {
		t.Error("the probe's report holds the graph pw-dump printed")
	}
}

// A pw-dump that hangs is stopped at the bound, and the report says
// so in its own words, not as the signal that stopped it.
func TestGraphAnswersReportsAPWDumpThatHangs(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := graphAnswers(ctx)

	want := "pw-dump did not finish in " + graphProbeTimeout.String()
	if err == nil || err.Error() != want {
		t.Errorf("the probe's report = %v, want %q", err, want)
	}
}
