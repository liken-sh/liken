package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckGraphPassesWhenPWDumpSucceeds(t *testing.T) {
	stubProbeDump(t, "0")
	if err := checkGraph(context.Background()); err != nil {
		t.Fatalf("checkGraph returned %v, want nil because pw-dump exits 0", err)
	}
}

func TestCheckGraphReportsTheFailureWithoutTheGraph(t *testing.T) {
	stubProbeDump(t, "3")
	err := checkGraph(context.Background())
	if err == nil {
		t.Fatal("checkGraph returned nil, want an error because pw-dump exits 3")
	}
	want := "running pw-dump: exit status 3: remote error: Connection refused"
	if err.Error() != want {
		t.Errorf("the probe's report = %q, want %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), `"id"`) {
		t.Error("the probe's report contains the graph that pw-dump printed, want only pw-dump's stderr")
	}
}

func TestCheckGraphReportsAPWDumpThatHangs(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "pw-dump"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the stub pw-dump: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := checkGraph(ctx)

	want := "pw-dump did not finish in " + graphProbeTimeout.String()
	if err == nil || err.Error() != want {
		t.Errorf("the probe's report = %v, want %q", err, want)
	}
}
