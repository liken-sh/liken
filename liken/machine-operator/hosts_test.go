package main

// applyHostEntries reconciles /etc/hosts against the spec, the way
// applySysctlSet reconciles the kernel's parameters: read the actual
// file, write only when it differs from the desired render, and
// report the entries the file actually holds afterward.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/machine"
)

func TestApplyHostEntriesWritesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas", "nas.home.arpa"}}}

	observed, err := applyHostEntries(path, "node-1", desired)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("got %+v, want %+v", observed, desired)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != machine.HostsFile("node-1", desired) {
		t.Errorf("the file does not match the shared renderer:\n%s", raw)
	}
}

func TestApplyHostEntriesSkipsAConvergedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}
	if err := os.WriteFile(path, []byte(machine.HostsFile("node-1", desired)), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	observed, err := applyHostEntries(path, "node-1", desired)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("a converged file still reports its entries: %+v", observed)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("a converged file must not be written: mtime moved from %v to %v", before.ModTime(), after.ModTime())
	}
}

func TestApplyHostEntriesHealsAHandEditedFile(t *testing.T) {
	// A file an outside edit changed differs from the rendered file,
	// so this pass rewrites it back to the spec's shape. This is the
	// healing property the write-on-divergence rule exists for.
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n::1 localhost\n127.0.1.1 node-1\n203.0.113.7 rogue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desired := []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}

	observed, err := applyHostEntries(path, "node-1", desired)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observed, desired) {
		t.Errorf("the healed file reports the spec's entries: %+v", observed)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != machine.HostsFile("node-1", desired) {
		t.Errorf("the file must be rewritten to the spec's shape:\n%s", raw)
	}
}

func TestApplyHostEntriesReportsNoEntriesDeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	observed, err := applyHostEntries(path, "node-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if observed != nil {
		t.Errorf("no entries declared, none observed: %+v", observed)
	}
}

func TestApplyHostEntriesReportsAWriteError(t *testing.T) {
	// A missing file reads as fs.ErrNotExist, which this function
	// treats as maximally divergent rather than an error, so it goes
	// on to write. A parent directory that does not exist either
	// makes that write fail, and the failure must come back to the
	// caller.
	path := filepath.Join(t.TempDir(), "missing-parent", "hosts")
	if _, err := applyHostEntries(path, "node-1", []machine.HostEntry{{Address: "10.10.0.20", Names: []string{"nas"}}}); err == nil {
		t.Error("expected an error when the file cannot be written")
	}
}

func TestApplyHostEntriesReportsAReadError(t *testing.T) {
	// A path that is itself a directory can never be read as a file.
	// This is the read failure the write-on-divergence rule must
	// still surface, rather than silently trying to overwrite it.
	path := t.TempDir()
	if _, err := applyHostEntries(path, "node-1", nil); err == nil {
		t.Error("expected an error when the file cannot be read")
	}
}
