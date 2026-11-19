package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

func TestApplyHostEntriesWritesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas", "nas.home.arpa"}}}

	observed, err := applyHostEntries(path, "node-1", desired, nil)
	if err != nil {
		t.Fatalf("applying the host entries: %v", err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("the observed entries are %+v, want %+v", observed, desired)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the hosts file: %v", err)
	}
	if string(raw) != machine.HostsFile("node-1", desired) {
		t.Errorf("the file does not match the shared renderer:\n%s", raw)
	}
}

func TestApplyHostEntriesSkipsAConvergedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	if err := os.WriteFile(path, []byte(machine.HostsFile("node-1", desired)), 0o644); err != nil {
		t.Fatalf("writing the hosts file: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the hosts file status: %v", err)
	}

	observed, err := applyHostEntries(path, "node-1", desired, nil)
	if err != nil {
		t.Fatalf("applying the host entries: %v", err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("the observed entries are %+v, want the desired entries for a file that already matches", observed)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the hosts file status: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the modification time of a converged file changed from %v to %v, want no write", before.ModTime(), after.ModTime())
	}
}

func TestApplyHostEntriesRewritesAnEditedFile(t *testing.T) {
	// A file that something else edited differs from the render, so the
	// pass rewrites it from the spec.
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n::1 localhost\n127.0.1.1 node-1\n203.0.113.7 rogue\n"), 0o644); err != nil {
		t.Fatalf("writing the hosts file: %v", err)
	}
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}

	observed, err := applyHostEntries(path, "node-1", desired, nil)
	if err != nil {
		t.Fatalf("applying the host entries: %v", err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("the rewritten file reports the entries %+v, want the spec's entries", observed)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the hosts file: %v", err)
	}
	if string(raw) != machine.HostsFile("node-1", desired) {
		t.Errorf("the file is\n%s\nwant it rewritten to the declared entries", raw)
	}
}

func TestApplyHostEntriesReportsNoEntriesDeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	observed, err := applyHostEntries(path, "node-1", nil, nil)
	if err != nil {
		t.Fatalf("applying the host entries: %v", err)
	}
	if observed != nil {
		t.Errorf("the observed entries are %+v, want none when none are declared", observed)
	}
}

func TestApplyHostEntriesReportsAWriteError(t *testing.T) {
	// A missing file is not a read error, so the function goes on to
	// write. The parent directory does not exist either, so the write
	// fails, and the function must return that error.
	path := filepath.Join(t.TempDir(), "missing-parent", "hosts")
	if _, err := applyHostEntries(path, "node-1", []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}, nil); err == nil {
		t.Error("applyHostEntries returned no error for a file in a missing directory, want a write error")
	}
}

func TestApplyHostEntriesReportsAReadError(t *testing.T) {
	// A directory cannot be read as a file. The function must return
	// that read error and not try to overwrite the directory.
	path := t.TempDir()
	if _, err := applyHostEntries(path, "node-1", nil, nil); err == nil {
		t.Error("applyHostEntries returned no error for a directory in place of the file, want a read error")
	}
}

// A line with an address and no name is not an entry.
func TestParseHostEntriesSkipsALineWithNoName(t *testing.T) {
	raw := machine.HostsFile("node-1", nil) + "10.0.0.9\n10.0.0.1 nas\n"

	got := parseHostEntries([]byte(raw))

	if len(got) != 1 || got[0].Address != "10.0.0.1" {
		t.Errorf("the parsed entries are %+v, want the nas entry alone", got)
	}
}
