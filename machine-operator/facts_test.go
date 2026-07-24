package main

// The operator reads init's facts from the tree under /run/liken/facts.
// These tests point the factsTree seam at a tempdir and prove that a
// tree init could have written reads back as the same facts the
// operator overlays its own observations onto.

import (
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/machine"
)

// writeFacts builds a facts tree in a tempdir from a status, by calling
// every per-subtree writer with the matching fields. It is the operator
// test's stand-in for init: init owns these writers and spreads them
// across its boot, so a test composes them in one place. The tree
// exists so each fact has one owner, so this compose-all pattern lives
// only in the tests. It returns the tree the seam should read.
func writeFacts(t *testing.T, s *machine.MachineStatus) machine.FactsTree {
	t.Helper()
	tree := machine.FactsTree{Dir: t.TempDir()}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tree.WriteRole(s.Role))
	must(tree.WriteLastCrash(s.LastCrash))
	must(tree.WriteVersion(s.Version))
	must(tree.WriteNetwork(s.Network))
	must(tree.WriteTime(s.Time))
	must(tree.WriteHardwareBasics(s.Hardware.CPUs, s.Hardware.MemoryBytes))
	must(tree.WriteBlockDevices(s.Hardware.BlockDevices))
	must(tree.WriteUnclaimed(s.Hardware.Unclaimed))
	must(tree.WriteFirmware(s.Firmware))
	must(tree.WriteStorage(s.Storage))
	must(tree.WriteModules(s.Modules))
	must(tree.WriteFeatures(s.Features))
	must(tree.WriteRegistries(s.Registries))
	must(tree.WriteBootTime(s.Boot.Time))
	must(tree.WriteBootSlot(s.Boot.Slot))
	must(tree.WriteBootStorage(s.Boot.Storage))
	must(tree.WriteBootModules(s.Boot.Modules))
	must(tree.WriteBootManifest(s.Boot.ManifestSource, s.Boot.ManifestHash))
	must(tree.WriteBootClusterManifest(s.Boot.ClusterManifestSource, s.Boot.ClusterManifestHash))
	must(tree.WriteBootCredentials(s.Boot.CredentialsSource, s.Boot.CredentialsHash))
	must(tree.WriteBootImports(s.Boot.ImportsSource, s.Boot.ImportsHash, s.Boot.ImportsDiscarded))
	must(tree.WriteBootRestarts(s.Boot.Restarts))
	must(tree.WriteRejection(machine.RejectMachine, s.Boot.Rejection))
	must(tree.WriteRejection(machine.RejectCluster, s.Boot.ClusterRejection))
	must(tree.WriteRejection(machine.RejectSystem, s.Boot.SystemRejection))
	must(tree.WriteRejection(machine.RejectCredentials, s.Boot.CredentialsRejection))
	return tree
}

func TestOperatorReadsFactsFromTheTree(t *testing.T) {
	booted := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	storage := machine.AllRolesInMemory()
	storage.ClusterState = machine.StorageRoleStatus{
		Backing: machine.BackingPartition, Device: "vda1",
		Partition: "liken:clusterState", CapacityBytes: 2_146_435_072,
	}
	want := &machine.MachineStatus{
		Role:     api.RoleLeader,
		Version:  machine.VersionStatus{Liken: "0.1.0", Kernel: "6.15.4"},
		Network:  machine.NetworkStatus{Interface: "eth0", Addresses: []string{"10.0.2.15/24"}},
		Hardware: machine.HardwareStatus{CPUs: 4, MemoryBytes: 4_294_967_296},
		Storage:  storage,
		Modules:  []machine.ModuleStatus{{Name: "overlay", State: machine.ModuleLoaded}},
		Boot: machine.BootStatus{
			Time: &booted,
			Slot: "A", ManifestSource: machine.ManifestSourceProven, ManifestHash: "abc123",
		},
	}

	restore := factsTree
	factsTree = writeFacts(t, want)
	t.Cleanup(func() { factsTree = restore })

	got, err := factsTree.Read()
	if err != nil {
		t.Fatal(err)
	}

	// The facts ride through the seam unchanged.
	if got.Role != want.Role || got.Boot.ManifestSource != machine.ManifestSourceProven || got.Boot.ManifestHash != "abc123" {
		t.Errorf("the boot identity must round-trip: %+v", got.Boot)
	}
	if got.Boot.Time == nil || !got.Boot.Time.Equal(booted) {
		t.Errorf("the boot time must round-trip: %v", got.Boot.Time)
	}
	if got.Hardware.CPUs != 4 || got.Storage.ClusterState.Backing != machine.BackingPartition {
		t.Errorf("the hardware and storage facts must round-trip: %+v", got)
	}
	if len(got.Modules) != 1 || got.Modules[0].Name != "overlay" {
		t.Errorf("the module outcomes must round-trip: %+v", got.Modules)
	}

	// The operator owns phase, conditions, and sysctls, which init never
	// writes. They read back at their zero value, ready for the operator
	// to overlay.
	if got.Phase != "" || got.Conditions != nil || got.Sysctls != nil {
		t.Errorf("the operator-owned fields must read as zero: %+v", got)
	}
}

func TestOperatorReportsAMissingFactsTree(t *testing.T) {
	// A missing tree is a fact the operator must report, not a default
	// it may invent. Read returns an error the reconcile pass turns into
	// a condition.
	restore := factsTree
	factsTree = machine.FactsTree{Dir: t.TempDir() + "/absent"}
	t.Cleanup(func() { factsTree = restore })

	if _, err := factsTree.Read(); err == nil {
		t.Error("a missing facts tree must be an error")
	}
}
