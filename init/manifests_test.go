package main

// Tests for manifest selection: finding the machineState partition
// before any spec exists, and the attempt-order policy. The peek's
// mount/unmount and the settle loop's actuation are QEMU territory;
// the decisions they act on are pinned here.

import (
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
	seed := &manifestChoice{source: machine.ManifestSourceSeed}

	cases := []struct {
		name string
		c    manifestCandidates
		want []string
	}{
		// The normal steady state: nothing staged, boot the proven.
		{"proven only", manifestCandidates{proven: proven}, []string{"Proven"}},
		// An edit awaits its proving boot, with the last-known-good behind it.
		{"staged then proven", manifestCandidates{staged: staged, proven: proven}, []string{"Staged", "Proven"}},
		// A staged file with no proven behind it: nothing to fall back to.
		{"staged alone", manifestCandidates{staged: staged}, []string{"Staged"}},
		// First boot: the seed's one appearance.
		{"first boot", manifestCandidates{}, []string{"Seed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, choice := range attemptOrder(c.c, seed) {
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
