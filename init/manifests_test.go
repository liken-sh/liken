package main

// Tests for manifest selection: finding the machineState partition
// before any spec exists, and the attempt-order policy. The peek's
// mount/unmount and the settle loop's actuation are QEMU territory;
// the decisions they act on are pinned here.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

func TestFindMachineStatePartition(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vda", "vda2", "liken:clusterState", 1<<29)

	p, err := findMachineStatePartition()
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.name != "vda1" {
		t.Errorf("expected vda1, got %+v", p)
	}
}

func TestFindMachineStatePartitionAbsentIsAFirstBoot(t *testing.T) {
	fakeMachine(t)
	p, err := findMachineStatePartition()
	if err != nil || p != nil {
		t.Errorf("no partition should mean nil, nil: %v %v", p, err)
	}
}

func TestFindMachineStatePartitionRefusesDuplicates(t *testing.T) {
	// A cloned or transplanted disk: two partitions carry the name,
	// and guessing which holds the real manifests could boot the
	// machine under a stranger's configuration.
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 1<<30, nil)
	addDisk(t, sys, dev, "vdb", 1<<30, nil)
	addPartition(t, sys, "vda", "vda1", "liken:machineState", 64<<20)
	addPartition(t, sys, "vdb", "vdb1", "liken:machineState", 64<<20)

	_, err := findMachineStatePartition()
	if err == nil {
		t.Fatal("expected a refusal for duplicate machineState partitions")
	}
	for _, want := range []string{"vda1", "vdb1", "refusing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestAttemptOrder(t *testing.T) {
	staged := &manifestChoice{source: machine.ManifestSourceStaged}
	proven := &manifestChoice{source: machine.ManifestSourceProven}

	cases := []struct {
		name string
		c    manifestCandidates
		want []machine.ManifestSource
	}{
		// The normal steady state: nothing staged, boot the proven.
		{"proven only", manifestCandidates{proven: proven}, []machine.ManifestSource{"Proven"}},
		// An edit awaits its proving boot, with the last-known-good behind it.
		{"staged then proven", manifestCandidates{staged: staged, proven: proven}, []machine.ManifestSource{"Staged", "Proven"}},
		// A staged file with no proven behind it: nothing to fall back to.
		{"staged alone", manifestCandidates{staged: staged}, []machine.ManifestSource{"Staged"}},
		// First boot: no durable candidates at all, which is
		// settleStorage's cue to consult the image's seed.
		{"first boot", manifestCandidates{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []machine.ManifestSource
			for _, choice := range attemptOrder(c.c) {
				got = append(got, choice.source)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("attempt %d: got %s, want %s", i, got[i], c.want[i])
				}
			}
		})
	}
}

// seedDir builds a manifests directory with the given file names, so
// each selection case reads as data.
func seedDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSeedPathSelection(t *testing.T) {
	cases := []struct {
		name      string
		files     []string
		requested string
		want      string // the base name of the chosen path; "" for none
		wantErr   string // a fragment the error must carry; "" for success
	}{
		{"explicit selection", []string{"node-1.yaml", "node-2.yaml"}, "node-2", "node-2.yaml", ""},
		{"explicit selection among one", []string{"node-1.yaml"}, "node-1", "node-1.yaml", ""},
		{"a lone manifest needs no name", []string{"node-1.yaml"}, "", "node-1.yaml", ""},
		{"no manifests is a valid machine", nil, "", "", ""},
		{"a name that matches nothing", []string{"node-1.yaml"}, "node-9", "", "liken.machine=node-9"},
		{"many manifests and no name", []string{"node-1.yaml", "node-2.yaml"}, "", "", "refusing to guess"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDir(t, c.files...)
			got, err := seedPath(dir, c.requested)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q, got path %q", c.wantErr, got)
				}
				if !errors.Is(err, errIdentity) {
					t.Errorf("selection failures should be identity errors: %v", err)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error should mention %q: %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if c.want != "" {
				want = filepath.Join(dir, c.want)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestSeedPathMissingDirectoryIsNoManifest(t *testing.T) {
	got, err := seedPath(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if err != nil || got != "" {
		t.Errorf("a missing directory should mean no manifest: %q, %v", got, err)
	}
}
