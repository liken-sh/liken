package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// addQueue gives a fake disk the queue attributes of its request
// size: the value in effect and the hardware's limit, in KiB.
func addQueue(t *testing.T, sys, disk, current, hardware string) {
	t.Helper()
	queue := filepath.Join(sys, disk, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("creating the fake queue directory: %v", err)
	}
	writeSysfs(t, queue, "max_sectors_kb", current+"\n")
	writeSysfs(t, queue, "max_hw_sectors_kb", hardware+"\n")
}

func readMaxSectors(t *testing.T, sys, disk string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sys, disk, "queue", "max_sectors_kb"))
	if err != nil {
		t.Fatalf("reading max_sectors_kb of %s: %v", disk, err)
	}
	return strings.TrimSpace(string(raw))
}

// clusterStateOn is the storage status of a boot that mounted
// clusterState from the partition vda5.
var clusterStateOn = func() machine.StorageStatus {
	s := machine.AllRolesInMemory()
	s.ClusterState = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda5", Partition: "liken:clusterState"}
	return s
}()

func TestApplyRequestSizesAtBoot(t *testing.T) {
	tests := []struct {
		name     string
		entries  []machine.DiskSettings
		storage  machine.StorageStatus
		vda, vdb string
	}{
		{"the default lands on the clusterState disk and no other", nil, clusterStateOn, "128", "4096"},
		{"clusterState in memory gets nothing", nil, machine.AllRolesInMemory(), "4096", "4096"},
		{"an override changes the clusterState disk",
			[]machine.DiskSettings{{Device: "/dev/vda", MaxRequestSize: "256Ki"}}, clusterStateOn, "256", "4096"},
		{"an override opts the clusterState disk out",
			[]machine.DiskSettings{{Device: "/dev/vda", MaxRequestSize: "Kernel"}}, clusterStateOn, "4096", "4096"},
		{"an override sets another disk",
			[]machine.DiskSettings{{Device: "/dev/vdb", MaxRequestSize: "512Ki"}}, clusterStateOn, "128", "512"},
		{"a value above the hardware limit is not written",
			[]machine.DiskSettings{{Device: "/dev/vdb", MaxRequestSize: "64Mi"}}, clusterStateOn, "128", "4096"},
		{"an absent disk changes nothing else",
			[]machine.DiskSettings{{Device: "/dev/vdz", MaxRequestSize: "512Ki"}}, clusterStateOn, "128", "4096"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys, dev := fakeMachine(t)
			addDisk(t, sys, dev, "vda", 8<<30, nil)
			addPartition(t, sys, "vda", "vda5", "liken:clusterState", 4<<30)
			addQueue(t, sys, "vda", "4096", "32767")
			addDisk(t, sys, dev, "vdb", 8<<30, nil)
			addQueue(t, sys, "vdb", "4096", "32767")

			applyRequestSizes(tt.entries, tt.storage)

			if got := readMaxSectors(t, sys, "vda"); got != tt.vda {
				t.Errorf("vda has max_sectors_kb %s, want %s", got, tt.vda)
			}
			if got := readMaxSectors(t, sys, "vdb"); got != tt.vdb {
				t.Errorf("vdb has max_sectors_kb %s, want %s", got, tt.vdb)
			}
		})
	}
}

func TestDiscoverBlockDevicesReadsTheRequestSizes(t *testing.T) {
	sys, dev := fakeMachine(t)
	addDisk(t, sys, dev, "vda", 8<<30, nil)
	addQueue(t, sys, "vda", "128", "32767")
	disks := discoverBlockDevices()
	if len(disks) != 1 || disks[0].MaxRequestSize != "128Ki" || disks[0].MaxHardwareRequestSize != "32767Ki" {
		t.Errorf("discoverBlockDevices() = %+v, want vda with 128Ki and 32767Ki", disks)
	}
}
