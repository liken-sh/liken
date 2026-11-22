package machine

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The disks that the planning tests resolve entries against: a SATA SSD
// that holds clusterState, a second disk, and an NVMe drive whose
// hardware limits requests to 128 KiB.
var planDisks = []BlockDevice{
	{Name: "sda", StableNames: []string{"/dev/disk/by-id/ata-SSD_1", "/dev/disk/by-path/pci-0000:00:17.0-ata-1"},
		MaxHardwareRequestSize: "32767Ki"},
	{Name: "sdb", StableNames: []string{"/dev/disk/by-id/ata-HDD_2"}, MaxHardwareRequestSize: "32767Ki"},
	{Name: "nvme0n1", StableNames: []string{"/dev/disk/by-id/nvme-NVME_3"}, MaxHardwareRequestSize: "128Ki"},
}

func TestPlanRequestSizes(t *testing.T) {
	tests := []struct {
		name         string
		entries      []DiskSettings
		clusterState string
		want         []RequestSizeTarget
	}{
		{
			name:         "the default lands on the clusterState disk",
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 128, Source: "the clusterState default"}},
		},
		{
			name: "a machine with clusterState in memory gets nothing",
		},
		{
			name:         "an override by kernel name changes the clusterState disk",
			entries:      []DiskSettings{{Device: "/dev/sda", MaxRequestSize: "512Ki"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 512, Source: "spec.disks[0]"}},
		},
		{
			name:         "an override by by-id name changes the clusterState disk",
			entries:      []DiskSettings{{Device: "/dev/disk/by-id/ata-SSD_1", MaxRequestSize: "1Mi"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 1024, Source: "spec.disks[0]"}},
		},
		{
			name:         "an override by by-path name changes the clusterState disk",
			entries:      []DiskSettings{{Device: "/dev/disk/by-path/pci-0000:00:17.0-ata-1", MaxRequestSize: "256Ki"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 256, Source: "spec.disks[0]"}},
		},
		{
			name:         "Kernel opts the clusterState disk out of the default",
			entries:      []DiskSettings{{Device: "/dev/sda", MaxRequestSize: "Kernel"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 0, Source: "spec.disks[0]"}},
		},
		{
			name:         "an override sets another disk and the default stays",
			entries:      []DiskSettings{{Device: "/dev/disk/by-id/ata-HDD_2", MaxRequestSize: "256Ki"}},
			clusterState: "sda",
			want: []RequestSizeTarget{
				{Disk: "sdb", KiB: 256, Source: "spec.disks[0]"},
				{Disk: "sda", KiB: 128, Source: "the clusterState default"},
			},
		},
		{
			name:         "an entry with no size leaves the default in place",
			entries:      []DiskSettings{{Device: "/dev/sda"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 128, Source: "the clusterState default"}},
		},
		{
			name:         "the default never asks for more than the hardware limit",
			clusterState: "nvme0n1",
			want:         []RequestSizeTarget{{Disk: "nvme0n1", KiB: 128, Source: "the clusterState default"}},
		},
		{
			name:         "a refused override leaves the default in place",
			entries:      []DiskSettings{{Device: "/dev/sda", MaxRequestSize: "64Mi"}},
			clusterState: "sda",
			want:         []RequestSizeTarget{{Disk: "sda", KiB: 128, Source: "the clusterState default"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problems := PlanRequestSizes(tt.entries, planDisks, tt.clusterState)
			if !slices.Equal(got, tt.want) {
				t.Errorf("PlanRequestSizes() = %+v, want %+v", got, tt.want)
			}
			// Only the refused override is a problem in this table, and
			// TestPlanRequestSizesRefuses covers the problems.
			if len(problems) > 1 {
				t.Errorf("PlanRequestSizes() reported %+v, want at most one problem", problems)
			}
		})
	}
}

// smallHardwareLimit is a disk behind a USB bridge whose hardware
// accepts requests of at most 120 KiB.
var smallHardwareLimit = []BlockDevice{{Name: "sdc", MaxHardwareRequestSize: "120Ki"}}

func TestPlanRequestSizesClampsTheDefaultToTheHardware(t *testing.T) {
	got, problems := PlanRequestSizes(nil, smallHardwareLimit, "sdc")
	want := []RequestSizeTarget{{Disk: "sdc", KiB: 120, Source: "the clusterState default"}}
	if !slices.Equal(got, want) || len(problems) != 0 {
		t.Errorf("PlanRequestSizes() = %+v, %+v, want %+v and no problems", got, problems, want)
	}
}

func TestPlanRequestSizesRefuses(t *testing.T) {
	tests := []struct {
		name    string
		entries []DiskSettings
		reason  string
	}{
		{"a value above the hardware limit", []DiskSettings{{Device: "/dev/nvme0n1", MaxRequestSize: "256Ki"}}, DiskAboveHardwareLimit},
		{"an absent disk", []DiskSettings{{Device: "/dev/disk/by-id/ata-GONE", MaxRequestSize: "256Ki"}}, DiskNotFound},
		{"an absent kernel name", []DiskSettings{{Device: "/dev/sdz", MaxRequestSize: "256Ki"}}, DiskNotFound},
		{"a value below the kernel's minimum", []DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "2Ki"}}, DiskSettingInvalid},
		{"a value that is not whole KiB", []DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "5000"}}, DiskSettingInvalid},
		{"a value that is not a size", []DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "128K"}}, DiskSettingInvalid},
		{"a zero value", []DiskSettings{{Device: "/dev/sdb", MaxRequestSize: "0"}}, DiskSettingInvalid},
		{"a by-uuid name", []DiskSettings{{Device: "/dev/disk/by-uuid/1234", MaxRequestSize: "256Ki"}}, DiskSettingInvalid},
		{"a name outside /dev", []DiskSettings{{Device: "sda", MaxRequestSize: "256Ki"}}, DiskSettingInvalid},
		{"two entries for one disk", []DiskSettings{
			{Device: "/dev/sdb", MaxRequestSize: "256Ki"},
			{Device: "/dev/disk/by-id/ata-HDD_2", MaxRequestSize: "512Ki"},
		}, DiskSettingInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, problems := PlanRequestSizes(tt.entries, planDisks, "")
			if len(problems) != 1 || problems[0].Reason != tt.reason {
				t.Errorf("PlanRequestSizes() reported %+v, want one problem with the reason %s", problems, tt.reason)
			}
		})
	}
}

func TestPlanRequestSizesKeepsTheFirstOfTwoEntriesForOneDisk(t *testing.T) {
	entries := []DiskSettings{
		{Device: "/dev/sdb", MaxRequestSize: "256Ki"},
		{Device: "/dev/disk/by-id/ata-HDD_2", MaxRequestSize: "512Ki"},
	}
	got, _ := PlanRequestSizes(entries, planDisks, "")
	want := []RequestSizeTarget{{Disk: "sdb", KiB: 256, Source: "spec.disks[0]"}}
	if !slices.Equal(got, want) {
		t.Errorf("PlanRequestSizes() = %+v, want %+v", got, want)
	}
}

// fakeSysBlock returns a temporary /sys/block with one disk, its queue
// attributes, and one partition.
func fakeSysBlock(t *testing.T, disk, partition, current, hardware string) string {
	t.Helper()
	dir := t.TempDir()
	queue := filepath.Join(dir, disk, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatalf("creating the queue directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, disk, partition), 0o755); err != nil {
		t.Fatalf("creating the partition directory: %v", err)
	}
	for name, value := range map[string]string{"max_sectors_kb": current, "max_hw_sectors_kb": hardware} {
		if err := os.WriteFile(filepath.Join(queue, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

func TestDiskOfPartition(t *testing.T) {
	dir := fakeSysBlock(t, "nvme0n1", "nvme0n1p5", "1280", "1280")
	disks := []BlockDevice{{Name: "sda"}, {Name: "nvme0n1"}}
	tests := []struct {
		partition, want string
	}{
		{"nvme0n1p5", "nvme0n1"},
		{"nvme0n1p9", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := DiskOfPartition(dir, tt.partition, disks); got != tt.want {
			t.Errorf("DiskOfPartition(%q) = %q, want %q", tt.partition, got, tt.want)
		}
	}
}

func TestWriteMaxRequestSizeChangesWhatReadsBack(t *testing.T) {
	dir := fakeSysBlock(t, "sda", "sda5", "4096", "32767")
	if err := WriteMaxRequestSize(dir, "sda", 128); err != nil {
		t.Fatalf("WriteMaxRequestSize() returned %v", err)
	}
	current, hardware, err := ReadRequestLimits(dir, "sda")
	if err != nil || current != 128 || hardware != 32767 {
		t.Errorf("ReadRequestLimits() = %d, %d, %v, want 128, 32767, nil", current, hardware, err)
	}
}

func TestWriteMaxRequestSizeCreatesNoFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteMaxRequestSize(dir, "sda", 128); err == nil {
		t.Error("WriteMaxRequestSize() on a disk with no queue directory returned nil, want an error")
	}
}

func TestFillRequestLimitsReportsTheValuesInEffect(t *testing.T) {
	dir := fakeSysBlock(t, "sda", "sda5", "128", "32767")
	d := BlockDevice{Name: "sda"}
	FillRequestLimits(dir, &d)
	if d.MaxRequestSize != "128Ki" || d.MaxHardwareRequestSize != "32767Ki" {
		t.Errorf("FillRequestLimits() set %q and %q, want 128Ki and 32767Ki", d.MaxRequestSize, d.MaxHardwareRequestSize)
	}
}

func TestFillRequestLimitsLeavesAnUnreadableDiskEmpty(t *testing.T) {
	d := BlockDevice{Name: "sda"}
	FillRequestLimits(t.TempDir(), &d)
	if d.MaxRequestSize != "" || d.MaxHardwareRequestSize != "" {
		t.Errorf("FillRequestLimits() set %q and %q, want both empty", d.MaxRequestSize, d.MaxHardwareRequestSize)
	}
}
