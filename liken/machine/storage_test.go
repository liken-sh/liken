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
				t.Fatalf("ParseSize(%q): %v", c.in, err)
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
				t.Errorf("ParseSize(%q) returned no error, want one", in)
			}
		})
	}
}

func TestSizeTextRendersTheLargestExactUnit(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  string
	}{
		{1 << 30, "1Gi"},
		{512 << 20, "512Mi"},
		{1 << 20, "1Mi"},
		{5 << 30, "5Gi"},
		{1536 << 20, "1536Mi"},
		{1 << 40, "1Ti"},
		{700, "700"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := SizeText(c.bytes); got != c.want {
				t.Errorf("SizeText(%d) = %q, want %q", c.bytes, got, c.want)
			}
		})
	}
}

func TestSizeTextRoundTripsThroughParseSize(t *testing.T) {
	for _, bytes := range []uint64{1 << 20, 1536 << 20, 6 << 30, 59 << 30, 1 << 40, 700} {
		got, err := ParseSize(SizeText(bytes))
		if err != nil {
			t.Fatalf("SizeText(%d) does not parse: %v", bytes, err)
		}
		if got != bytes {
			t.Errorf("ParseSize(SizeText(%d)) = %d, want %d", bytes, got, bytes)
		}
	}
}

func TestRolesAreOrderedAndSkipUndeclared(t *testing.T) {
	spec := StorageSpec{
		PodEphemeral: &StorageRole{Device: "/dev/vdb"},
		ClusterState: &StorageRole{Device: "/dev/vda"},
	}
	roles := spec.Roles()
	if len(roles) != 2 {
		t.Fatalf("Roles() has %d roles, want 2", len(roles))
	}
	if roles[0].Name != "clusterState" || roles[1].Name != "podEphemeral" {
		t.Errorf("Roles() is ordered %s, %s, want the order of StorageRoleNames", roles[0].Name, roles[1].Name)
	}
}

func TestRolesCanonicalOrder(t *testing.T) {
	one := &StorageRole{Device: "/dev/vda"}
	spec := StorageSpec{
		BIOSBoot:         one,
		BootHome:         one,
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
		t.Fatalf("Roles() has %d roles, want %d", len(roles), len(want))
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
		t.Errorf("PartitionName() = %q, want %q", got, "liken:clusterState")
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
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestValidateRequiresADevice(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted a role with no device, want an error")
	}
}

func TestValidateRejectsByUUIDDevices(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/disk/by-uuid/1234-5678", Size: "1Gi"}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate accepted a by-uuid device, want an error")
	}
	if !strings.Contains(err.Error(), "clusterState") || !strings.Contains(err.Error(), "/dev/disk/by-uuid/1234-5678") {
		t.Errorf("the error is %q, want it to name clusterState and the device", err)
	}
}

func TestValidateRejectsUnsupportedDiskTrees(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/disk/by-partlabel/x", Size: "1Gi"}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate accepted a by-partlabel device, want an error")
	}
	if !strings.Contains(err.Error(), "clusterState") || !strings.Contains(err.Error(), "/dev/disk/by-partlabel/x") {
		t.Errorf("the error is %q, want it to name clusterState and the device", err)
	}
}

func TestValidateRejectsATreeWithNoDiskName(t *testing.T) {
	for _, device := range []string{"/dev/disk/by-id", "/dev/disk/by-path"} {
		t.Run(device, func(t *testing.T) {
			spec := StorageSpec{ClusterState: &StorageRole{Device: device, Size: "1Gi"}}
			err := spec.Validate()
			if err == nil {
				t.Fatal("Validate accepted a disk tree with no disk name, want an error")
			}
			if !strings.Contains(err.Error(), "clusterState") || !strings.Contains(err.Error(), device) {
				t.Errorf("the error is %q, want it to name clusterState and the device", err)
			}
		})
	}
}

func TestValidateAcceptsStableDiskNames(t *testing.T) {
	cases := []string{
		"/dev/sda",
		"/dev/disk/by-id/wwn-0x5002538d40a45c88",
		"/dev/disk/by-path/pci-0000:00:1f.2-ata-1",
	}
	for _, device := range cases {
		t.Run(device, func(t *testing.T) {
			spec := StorageSpec{ClusterState: &StorageRole{Device: device, Size: "1Gi"}}
			if err := spec.Validate(); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", device, err)
			}
		})
	}
}

func TestValidateRejectsUnparseableSizes(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda", Size: "lots"}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted the size \"lots\", want an error")
	}
}

func TestValidateRejectsZeroSizes(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda", Size: "0Gi"}}
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted the size 0Gi, want an error")
	}
}

func TestValidateAllowsOnlyOneRemainderPerDevice(t *testing.T) {
	spec := StorageSpec{
		PodStorage:   &StorageRole{Device: "/dev/vdb"},
		PodEphemeral: &StorageRole{Device: "/dev/vdb"},
	}
	err := spec.Validate()
	if err == nil {
		t.Fatal("Validate accepted two roles with no size on /dev/vdb, want an error")
	}
	if !strings.Contains(err.Error(), "/dev/vdb") {
		t.Errorf("the error is %q, want it to name /dev/vdb", err)
	}
}

func TestSystemSlotDirs(t *testing.T) {
	if got := SystemSlotDir("A"); got != "/var/lib/liken/system/a" {
		t.Errorf("SystemSlotDir(\"A\") = %q, want /var/lib/liken/system/a", got)
	}
	if got := SystemSlotDir("B"); got != "/var/lib/liken/system/b" {
		t.Errorf("SystemSlotDir(\"B\") = %q, want /var/lib/liken/system/b", got)
	}
}

func TestInactiveSlot(t *testing.T) {
	cases := map[string]string{"A": "B", "B": "A", "": ""}
	for running, want := range cases {
		if got := InactiveSlot(running); got != want {
			t.Errorf("InactiveSlot(%q) = %q, want %q", running, got, want)
		}
	}
}

func TestSpecRoleUnknownNameIsNil(t *testing.T) {
	spec := StorageSpec{ClusterState: &StorageRole{Device: "/dev/vda"}}
	if role := spec.Role("bogus"); role != nil {
		t.Errorf("Role of a name outside StorageRoleNames is %+v, want nil", role)
	}
}
