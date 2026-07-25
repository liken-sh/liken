package machine

import (
	"strings"
	"testing"
)

// driftLabStorage returns the lab machine's storage shape: five
// roles across two disks. Tests reuse this fixture for drift
// comparisons.
func driftLabStorage() StorageSpec {
	return StorageSpec{
		MachineState:     &StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &StorageRole{Device: "/dev/vda"},
		PodStorage:       &StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &StorageRole{Device: "/dev/vdb"},
	}
}

func TestStorageDriftSeesNoDriftInTheSameSpec(t *testing.T) {
	if diffs := StorageDrift(driftLabStorage(), driftLabStorage()); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestStorageDriftNormalizesSizes(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "2048Mi" // the same size as 2Gi, written differently
	if diffs := StorageDrift(desired, driftLabStorage()); len(diffs) != 0 {
		t.Errorf("2048Mi and 2Gi are the same size: %v", diffs)
	}
}

func TestStorageDriftSeesAGrow(t *testing.T) {
	desired := driftLabStorage()
	desired.PodStorage.Size = "3Gi"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "podStorage") {
		t.Errorf("expected one podStorage diff: %v", diffs)
	}
}

func TestStorageDriftSeesAnAddedRole(t *testing.T) {
	actuated := driftLabStorage()
	actuated.PodStorage = nil
	diffs := StorageDrift(driftLabStorage(), actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "declared but not actuated") {
		t.Errorf("expected an added-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesARemovedRole(t *testing.T) {
	desired := driftLabStorage()
	desired.PodEphemeral = nil
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "no longer declared") {
		t.Errorf("expected a removed-role diff: %v", diffs)
	}
}

func TestStorageDriftSeesADeviceChange(t *testing.T) {
	desired := driftLabStorage()
	desired.ClusterState.Device = "/dev/vdc"
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "device") {
		t.Errorf("expected a device diff: %v", diffs)
	}
}

func TestStorageDriftFallsBackToStringsForUnparseableSizes(t *testing.T) {
	// Validation refuses these sizes anyway. Drift detection must not
	// panic on them. String equality is the only comparison left.
	desired := driftLabStorage()
	desired.PodStorage.Size = "a-whole-bunch"
	actuated := driftLabStorage()
	actuated.PodStorage.Size = "a-whole-bunch"
	if diffs := StorageDrift(desired, actuated); len(diffs) != 0 {
		t.Errorf("identical spellings should not drift, parseable or not: %v", diffs)
	}
	actuated.PodStorage.Size = "even-more"
	if diffs := StorageDrift(desired, actuated); len(diffs) != 1 {
		t.Errorf("different spellings should drift: %v", diffs)
	}
}

func TestStorageDriftNamesTheRemainder(t *testing.T) {
	// A remainder role's size is spelled "" in the spec. The diff
	// message must say "(remainder)" instead of showing nothing.
	desired := driftLabStorage()
	desired.ClusterState.Size = "3Gi" // held the remainder before this line
	diffs := StorageDrift(desired, driftLabStorage())
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(remainder)") {
		t.Errorf("expected the diff to name the remainder: %v", diffs)
	}
}

// driftLabNetwork returns the lab machine's network shape: a DHCP
// uplink, and a cluster segment with a static address on it. Tests
// reuse this fixture the way the storage tests reuse
// driftLabStorage.
func driftLabNetwork() NetworkSpec {
	return NetworkSpec{Interfaces: []InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
}

func TestNetworkDriftSeesNoDriftInTheSameSpec(t *testing.T) {
	actuated := driftLabNetwork()
	if diffs := NetworkDrift(driftLabNetwork(), &actuated); len(diffs) != 0 {
		t.Errorf("identical specs should not drift: %v", diffs)
	}
}

func TestNetworkDriftTreatsTwoEmptySpecsAsConverged(t *testing.T) {
	// A machine that declared nothing and still declares nothing takes
	// the zero-configuration default on both sides. If this case
	// drifted, every machine that never wrote a network stanza would
	// ask for a reboot forever.
	if diffs := NetworkDrift(NetworkSpec{}, &NetworkSpec{}); len(diffs) != 0 {
		t.Errorf("nothing declared, nothing actuated: %v", diffs)
	}
}

func TestNetworkDriftReportsNothingWithoutABootRecord(t *testing.T) {
	// A boot that recorded no network cannot be judged. Reading the
	// missing record as an empty spec would report every declared
	// interface as drift, and a fleet of such machines would all stage
	// a manifest and ask for a reboot at once.
	if diffs := NetworkDrift(driftLabNetwork(), nil); len(diffs) != 0 {
		t.Errorf("an unrecorded network is not drift: %v", diffs)
	}
}

func TestNetworkDriftSeesAnAddedInterface(t *testing.T) {
	actuated := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(driftLabNetwork(), &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "eth1 declared but not actuated") {
		t.Errorf("expected an added-interface diff: %v", diffs)
	}
}

func TestNetworkDriftSeesARemovedInterface(t *testing.T) {
	actuated := driftLabNetwork()
	desired := NetworkSpec{Interfaces: driftLabNetwork().Interfaces[:1]}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "actuated but no longer declared") {
		t.Errorf("expected a removed-interface diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedAddress(t *testing.T) {
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Address = "10.10.0.9/24"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "address 10.10.0.9/24 declared, 10.10.0.1/24 actuated") {
		t.Errorf("expected an address diff: %v", diffs)
	}
}

func TestNetworkDriftNamesTheDHCPDefault(t *testing.T) {
	// An interface with no address asks for DHCP. The diff must say
	// that word, because the person reading it has an empty field on
	// one side and needs to know what the empty field means.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Address = "192.168.1.5/24"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "(DHCP) actuated") {
		t.Errorf("expected the diff to name DHCP: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedNameserverList(t *testing.T) {
	// This is the edit the lab drilled: a nameserver added to an
	// interface that had none. Nameserver order is preference order,
	// so the list is compared as a list, not as a set.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nameservers 10.10.0.1 declared, (none) actuated") {
		t.Errorf("expected a nameservers diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAChangedGateway(t *testing.T) {
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1].Gateway = "10.10.0.254"
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "gateway 10.10.0.254 declared, (none) actuated") {
		t.Errorf("expected a gateway diff: %v", diffs)
	}
}

func TestNetworkDriftReportsAPortSwapOnce(t *testing.T) {
	// When a position names a different port, the addressing under it
	// belongs to that other port. Reporting each field as well would
	// say the same difference several times over.
	actuated := driftLabNetwork()
	desired := driftLabNetwork()
	desired.Interfaces[1] = InterfaceSpec{Name: "eth2", Address: "10.10.0.9/24"}
	diffs := NetworkDrift(desired, &actuated)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "interface 2: eth2 declared, eth1 actuated") {
		t.Errorf("expected one identity diff: %v", diffs)
	}
}

func TestNetworkDriftSeesAReorderedList(t *testing.T) {
	// Interface order is the order each interface's nameservers reach
	// resolv.conf, so the same two ports in the other order are a
	// different request.
	actuated := driftLabNetwork()
	desired := NetworkSpec{Interfaces: []InterfaceSpec{
		actuated.Interfaces[1], actuated.Interfaces[0],
	}}
	if diffs := NetworkDrift(desired, &actuated); len(diffs) != 2 {
		t.Errorf("a reorder is a change at both positions: %v", diffs)
	}
}

func TestModulesDriftIgnoresOrderAndRepetition(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia", "zram", "nvidia"}, []string{"zram", "nvidia"})
	if len(diffs) != 0 {
		t.Errorf("the lists are the same set: %v", diffs)
	}
}

func TestModulesDriftTreatsNilAndEmptyAlike(t *testing.T) {
	if diffs := ModulesDrift(nil, []string{}); len(diffs) != 0 {
		t.Errorf("nothing declared, nothing actuated: %v", diffs)
	}
}

func TestModulesDriftSeesAnAddedModule(t *testing.T) {
	diffs := ModulesDrift([]string{"nvidia"}, nil)
	if len(diffs) != 1 || !strings.Contains(diffs[0], "nvidia declared but this boot ran without it") {
		t.Errorf("got %v", diffs)
	}
}

func TestModulesDriftSeesARemovedModule(t *testing.T) {
	diffs := ModulesDrift(nil, []string{"zram"})
	if len(diffs) != 1 || !strings.Contains(diffs[0], "zram no longer declared") {
		t.Errorf("got %v", diffs)
	}
}
