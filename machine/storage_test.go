package machine

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"512", 512},
		{"64Ki", 65_536},
		{"100Mi", 104_857_600},
		{"2Gi", 2_147_483_648},
		{"1Ti", 1_099_511_627_776},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := ParseSize(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("ParseSize(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseSizeRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "Gi", "2GB", "2G", "-1Gi", "0x10", "2.5Gi", "0", "0Gi"} {
		t.Run(in, func(t *testing.T) {
			if _, err := ParseSize(in); err == nil {
				t.Errorf("ParseSize(%q) succeeded, want error", in)
			}
		})
	}
}

func TestRolesAreOrderedAndSkipUndeclared(t *testing.T) {
	spec := StorageSpec{
		PodEphemeral: &StorageRole{Device: "/dev/vdb"},
		ClusterState: &StorageRole{Device: "/dev/vda"},
	}
	roles := spec.Roles()
	if len(roles) != 2 {
		t.Fatalf("got %d roles", len(roles))
	}
	if roles[0].Name != "clusterState" || roles[1].Name != "podEphemeral" {
		t.Errorf("wrong order: %s, %s", roles[0].Name, roles[1].Name)
	}
}

// The canonical order is load-bearing twice: it's the partition
// layout when roles share a disk, and it front-loads the earliest
// readers — the system slots for the firmware, then machineState so
// the partition a boot must find before reading any spec leads the
// data roles.
func TestRolesCanonicalOrder(t *testing.T) {
	one := &StorageRole{Device: "/dev/vda"}
	spec := StorageSpec{
		SystemA:          one,
		SystemB:          one,
		MachineState:     one,
		MachineEphemeral: one,
		ClusterState:     one,
		PodStorage:       one,
		PodEphemeral:     one,
	}
	want := StorageRoleNames
	roles := spec.Roles()
	if len(roles) != len(want) {
		t.Fatalf("got %d roles, want %d", len(roles), len(want))
	}
	for i, w := range want {
		if roles[i].Name != w {
			t.Errorf("role %d is %s, want %s", i, roles[i].Name, w)
		}
	}
}

func TestPartitionNames(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda"}}
	if got := spec.Roles()[0].PartitionName(); got != "liken:clusterState" {
		t.Errorf("partition name: %q", got)
	}
}

func TestValidateAcceptsTheLabMachine(t *testing.T) {
	spec := StorageSpec{
		MachineState:     &StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &StorageRole{Device: "/dev/vda"},
		PodStorage:       &StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &StorageRole{Device: "/dev/vdb"},
	}
	if err := spec.Validate(); err != nil {
		t.Error(err)
	}
}

func TestStorageConditionAllPlaced(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda"}}
	status := AllRolesInMemory()
	status.ClusterState = StorageRoleStatus{Backing: BackingPartition, Device: "vda1"}
	c := StorageCondition(spec, status)
	if c.Type != "StorageReady" || c.Status != "True" || c.Reason != "AllRolesPlaced" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState on vda1") {
		t.Errorf("message should name the landing: %q", c.Message)
	}
}

func TestStorageConditionDeclaredButInMemory(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda"}}
	c := StorageCondition(spec, AllRolesInMemory())
	if c.Status != "False" || c.Reason != "RolesInMemory" {
		t.Errorf("got %+v", c)
	}
	if !strings.Contains(c.Message, "clusterState") {
		t.Errorf("message should name the role: %q", c.Message)
	}
}

func TestStorageConditionNothingDeclared(t *testing.T) {
	c := StorageCondition(StorageSpec{}, AllRolesInMemory())
	if c.Status != "True" || c.Reason != "NothingDeclared" {
		t.Errorf("got %+v", c)
	}
}

func TestValidateRequiresADevice(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for a role with no device")
	}
}

func TestValidateRejectsUnparseableSizes(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda", Size: "lots"}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for an unparseable size")
	}
}

func TestValidateRejectsZeroSizes(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda", Size: "0Gi"}}
	if err := spec.Validate(); err == nil {
		t.Error("expected an error for a zero size")
	}
}

func TestValidateAllowsOnlyOneRemainderPerDevice(t *testing.T) {
	spec := StorageSpec{
		PodStorage:   &StorageRole{Device: "/dev/vdb"},
		PodEphemeral: &StorageRole{Device: "/dev/vdb"},
	}
	err := spec.Validate()
	if err == nil {
		t.Fatal("expected an error for two sizeless roles on one device")
	}
	if !strings.Contains(err.Error(), "/dev/vdb") {
		t.Errorf("error should name the device: %v", err)
	}
}

func TestSystemSlotDirs(t *testing.T) {
	if got := SystemSlotDir("A"); got != "/var/lib/liken/system/a" {
		t.Errorf("slot A: %q", got)
	}
	if got := SystemSlotDir("B"); got != "/var/lib/liken/system/b" {
		t.Errorf("slot B: %q", got)
	}
}

func TestInactiveSlot(t *testing.T) {
	cases := map[string]string{"A": "B", "B": "A", "": ""}
	for running, want := range cases {
		if got := InactiveSlot(running); got != want {
			t.Errorf("InactiveSlot(%q): got %q, want %q", running, got, want)
		}
	}
}
