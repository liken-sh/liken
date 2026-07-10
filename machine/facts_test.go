package machine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// someFacts is a fully-populated status block, the shape init writes
// after a successful boot.
func someFacts(t *testing.T) *MachineStatus {
	t.Helper()
	booted := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	expires := booted.Add(time.Hour)
	return &MachineStatus{
		Version: VersionStatus{Liken: "0.1.0", Kernel: "6.15.4", Xtables: "v1.8.11 (legacy)"},
		Network: NetworkStatus{
			Interface:    "eth0",
			MAC:          "52:54:00:12:34:56",
			Addresses:    []string{"10.0.2.15/24"},
			Gateway:      "10.0.2.2",
			Nameservers:  []string{"10.0.2.3"},
			LeaseExpires: &expires,
		},
		Hardware: HardwareStatus{
			CPUs:        4,
			MemoryBytes: 4_294_967_296,
			BlockDevices: []BlockDevice{
				{Name: "vda", SizeBytes: 2_147_483_648, Serial: "liken-lab-state"},
				{Name: "sda", SizeBytes: 4_294_967_296, Model: "QEMU HARDDISK"},
			},
		},
		Storage: func() StorageStatus {
			s := AllRolesInMemory()
			s.ClusterState = StorageRoleStatus{
				Backing: BackingPartition, Device: "vda1",
				Partition: "liken:clusterState", CapacityBytes: 2_146_435_072,
			}
			return s
		}(),
		BootedAt: &booted,
	}
}

func TestFactsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liken", "facts.yaml")
	written := someFacts(t)
	if err := WriteFacts(path, written); err != nil {
		t.Fatal(err)
	}
	read, err := ReadFacts(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(written, read) {
		t.Errorf("round trip changed the facts:\nwrote %+v\nread  %+v", written, read)
	}
}

func TestReadFactsMissingFile(t *testing.T) {
	if _, err := ReadFacts(filepath.Join(t.TempDir(), "facts.yaml")); err == nil {
		t.Fatal("expected an error for a missing facts file")
	}
}

func TestReadFactsRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "facts.yaml")
	if err := os.WriteFile(path, []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFacts(path); err == nil {
		t.Error("expected an error for an unparseable facts file")
	}
}

func TestWriteFactsReportsAnUnwritableDirectory(t *testing.T) {
	path := filepath.Join(readOnlyDir(t), "facts.yaml")
	if err := WriteFacts(path, &MachineStatus{}); err == nil {
		t.Error("expected an error writing facts into a read-only directory")
	}
}

func TestWriteFactsReportsAnUncreatableDirectory(t *testing.T) {
	path := filepath.Join(readOnlyDir(t), "liken", "facts.yaml")
	if err := WriteFacts(path, &MachineStatus{}); err == nil {
		t.Error("expected an error when the facts directory can't be created")
	}
}

func TestWriteFactsReportsAPathThatIsADirectory(t *testing.T) {
	if err := WriteFacts(t.TempDir(), &MachineStatus{}); err == nil {
		t.Error("expected an error writing facts over an existing directory")
	}
}
