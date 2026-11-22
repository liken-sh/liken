package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

// diskMachine is a fake /sys/block with two disks that the kernel left
// at its own request size: sda, which holds clusterState on sda5, and
// sdb. It returns the directory and the facts that init would publish
// for the two disks. The directory is named block, so its parent can
// stand for /sys in a whole pass.
func diskMachine(t *testing.T) (string, *machine.MachineStatus) {
	t.Helper()
	sysBlock := filepath.Join(t.TempDir(), "block")
	for _, disk := range []string{"sda", "sdb"} {
		queue := filepath.Join(sysBlock, disk, "queue")
		if err := os.MkdirAll(queue, 0o755); err != nil {
			t.Fatalf("creating the queue of %s: %v", disk, err)
		}
		writeQueue(t, sysBlock, disk, "max_sectors_kb", "4096")
		writeQueue(t, sysBlock, disk, "max_hw_sectors_kb", "32767")
	}
	if err := os.MkdirAll(filepath.Join(sysBlock, "sda", "sda5"), 0o755); err != nil {
		t.Fatalf("creating the partition sda5: %v", err)
	}
	facts := &machine.MachineStatus{
		Hardware: machine.HardwareStatus{BlockDevices: []machine.BlockDevice{
			{Name: "sda", StableNames: []string{"/dev/disk/by-id/ata-SSD_1"}, MaxRequestSize: "4Mi", MaxHardwareRequestSize: "32767Ki"},
			{Name: "sdb", StableNames: []string{"/dev/disk/by-id/ata-HDD_2"}, MaxRequestSize: "4Mi", MaxHardwareRequestSize: "32767Ki"},
		}},
		Storage: machine.AllRolesInMemory(),
	}
	facts.Storage.ClusterState = machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "sda5"}
	return sysBlock, facts
}

func writeQueue(t *testing.T, sysBlock, disk, attribute, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(sysBlock, disk, "queue", attribute), []byte(value+"\n"), 0o644); err != nil {
		t.Fatalf("writing %s of %s: %v", attribute, disk, err)
	}
}

func readQueue(t *testing.T, sysBlock, disk string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sysBlock, disk, "queue", "max_sectors_kb"))
	if err != nil {
		t.Fatalf("reading max_sectors_kb of %s: %v", disk, err)
	}
	return strings.TrimSpace(string(raw))
}

func TestApplyDiskSettings(t *testing.T) {
	tests := []struct {
		name     string
		entries  []machine.DiskSettings
		sda, sdb string
		status   api.ConditionStatus
		reason   string
	}{
		{"the default lands on the clusterState disk and no other", nil,
			"128", "4096", api.ConditionTrue, "Applied"},
		{"an override changes the clusterState disk",
			[]machine.DiskSettings{{Device: "/dev/disk/by-id/ata-SSD_1", MaxRequestSize: "512Ki"}},
			"512", "4096", api.ConditionTrue, "Applied"},
		// A write of 0 asks the kernel to compute its own value again.
		// The fake sysfs keeps what was written, so the 0 is the request.
		{"an override opts the clusterState disk out",
			[]machine.DiskSettings{{Device: "/dev/sda", MaxRequestSize: "Kernel"}},
			"0", "4096", api.ConditionTrue, "Applied"},
		{"an override sets another disk",
			[]machine.DiskSettings{{Device: "/dev/disk/by-id/ata-HDD_2", MaxRequestSize: "256Ki"}},
			"128", "256", api.ConditionTrue, "Applied"},
		{"a value above the hardware limit",
			[]machine.DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "64Mi"}},
			"128", "4096", api.ConditionFalse, "AboveHardwareLimit"},
		{"an absent disk",
			[]machine.DiskSettings{{Device: "/dev/disk/by-id/ata-GONE", MaxRequestSize: "256Ki"}},
			"128", "4096", api.ConditionFalse, "DiskNotFound"},
		{"a value that admission refuses, from a manifest on a stick",
			[]machine.DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "1Ki"}},
			"128", "4096", api.ConditionFalse, "InvalidSetting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sysBlock, facts := diskMachine(t)
			result := applyDiskSettings(sysBlock, tt.entries, facts, &passOutcome{}, newRequestSizeMemory())
			if got := readQueue(t, sysBlock, "sda"); got != tt.sda {
				t.Errorf("sda has max_sectors_kb %s, want %s", got, tt.sda)
			}
			if got := readQueue(t, sysBlock, "sdb"); got != tt.sdb {
				t.Errorf("sdb has max_sectors_kb %s, want %s", got, tt.sdb)
			}
			c := diskSettingsCondition(result)
			if c.Status != tt.status || c.Reason != tt.reason {
				t.Errorf("the condition is %s/%s (%s), want %s/%s", c.Status, c.Reason, c.Message, tt.status, tt.reason)
			}
		})
	}
}

func TestApplyDiskSettingsWritesNothingOnASecondPass(t *testing.T) {
	tests := []struct {
		name    string
		entries []machine.DiskSettings
	}{
		{"the default", nil},
		{"a disk returned to the kernel's value", []machine.DiskSettings{{Device: "/dev/sda", MaxRequestSize: "Kernel"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sysBlock, facts := diskMachine(t)
			mem := newRequestSizeMemory()
			applyDiskSettings(sysBlock, tt.entries, facts, &passOutcome{}, mem)
			second := &passOutcome{}
			applyDiskSettings(sysBlock, tt.entries, facts, second, mem)
			if len(second.writes) != 0 {
				t.Errorf("the second pass wrote %v, want no writes", second.writes)
			}
		})
	}
}

func TestApplyDiskSettingsWritesAChangedValueBack(t *testing.T) {
	sysBlock, facts := diskMachine(t)
	mem := newRequestSizeMemory()
	applyDiskSettings(sysBlock, nil, facts, &passOutcome{}, mem)
	writeQueue(t, sysBlock, "sda", "max_sectors_kb", "1280")
	applyDiskSettings(sysBlock, nil, facts, &passOutcome{}, mem)
	if got := readQueue(t, sysBlock, "sda"); got != "128" {
		t.Errorf("sda has max_sectors_kb %s after another program changed it, want 128", got)
	}
}

func TestApplyDiskSettingsWithoutFactsIsUnknown(t *testing.T) {
	result := applyDiskSettings(t.TempDir(), nil, nil, &passOutcome{}, newRequestSizeMemory())
	if c := diskSettingsCondition(result); c.Status != api.ConditionUnknown {
		t.Errorf("the condition is %s/%s, want Unknown", c.Status, c.Reason)
	}
}

func TestApplyDiskSettingsWithClusterStateInMemory(t *testing.T) {
	sysBlock, facts := diskMachine(t)
	facts.Storage = machine.AllRolesInMemory()
	result := applyDiskSettings(sysBlock, nil, facts, &passOutcome{}, newRequestSizeMemory())
	if got := readQueue(t, sysBlock, "sda"); got != "4096" {
		t.Errorf("sda has max_sectors_kb %s, want the kernel's 4096", got)
	}
	if c := diskSettingsCondition(result); c.Status != api.ConditionTrue || c.Reason != "NothingToApply" {
		t.Errorf("the condition is %s/%s, want True/NothingToApply", c.Status, c.Reason)
	}
}

func TestRequestSizesInEffectReadsBackEachDisk(t *testing.T) {
	sysBlock, facts := diskMachine(t)
	applyDiskSettings(sysBlock, nil, facts, &passOutcome{}, newRequestSizeMemory())
	disks := requestSizesInEffect(sysBlock, facts.Hardware.BlockDevices)
	want := map[string][2]string{"sda": {"128Ki", "32767Ki"}, "sdb": {"4Mi", "32767Ki"}}
	for _, d := range disks {
		if got := [2]string{d.MaxRequestSize, d.MaxHardwareRequestSize}; got != want[d.Name] {
			t.Errorf("%s reports %v, want %v", d.Name, got, want[d.Name])
		}
	}
	if facts.Hardware.BlockDevices[0].MaxRequestSize != "4Mi" {
		t.Error("requestSizesInEffect changed the facts it read, want a new list")
	}
}

func TestRequestSizeCheckFindsAChangedDisk(t *testing.T) {
	sysBlock, facts := diskMachine(t)
	result := applyDiskSettings(sysBlock, nil, facts, &passOutcome{}, newRequestSizeMemory())
	check := sysctlCheck{requestSizes: result.applied}
	if check.requestSizesDrifted(sysBlock) {
		t.Error("the check found drift right after the pass, want none")
	}
	writeQueue(t, sysBlock, "sda", "max_sectors_kb", "1280")
	if !check.requestSizesDrifted(sysBlock) {
		t.Error("the check found no drift after another program changed sda, want drift")
	}
}

// A whole pass applies the default and publishes the value that sysfs
// reads back after the write, not the value that init published.
func TestAPassPublishesTheRequestSizesInEffect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		isolatePass(t)
		sysBlock, facts := diskMachine(t)
		draSysfsRoot = filepath.Dir(sysBlock)
		factsTree = writeFacts(t, facts)
		fake := newPassAPI()
		client, _ := passClients(t, fake)

		_, m := runPasses(t, fake, &reader{client: client})

		got := map[string]string{}
		for _, d := range m.Status.Hardware.BlockDevices {
			got[d.Name] = d.MaxRequestSize
		}
		if got["sda"] != "128Ki" || got["sdb"] != "4Mi" {
			t.Errorf("the status reports the request sizes %v, want sda at 128Ki and sdb at 4Mi", got)
		}
		condition := api.FindCondition(m.Status.Conditions, diskSettingsAppliedCondition)
		if condition == nil || condition.Status != api.ConditionTrue {
			t.Errorf("the status has the condition %+v, want DiskSettingsApplied True", condition)
		}
	})
}
