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
