package machine

// spec.disks sets attributes of a whole disk's request queue, which the
// kernel exposes under /sys/block/<disk>/queue. The one attribute that
// it sets is max_sectors_kb, the largest single request that the block
// layer sends to the disk. A setting belongs to a whole disk and not to
// a storage role, because several roles can share one disk and could
// then ask for different values.
//
// Every machine sets max_sectors_kb on the disk that holds clusterState
// to ClusterStateMaxRequestSize, unless an entry of spec.disks sets a
// maxRequestSize for that disk.
// Mainline Linux caps the kernel's own value at BLK_DEF_MAX_SECTORS_CAP
// in block/blk.h, which is far larger. When a pod writes hard to a disk
// that also holds etcd, the queue fills with requests of that size, and
// etcd's small WAL fsync waits behind all of them. A consumer SSD that
// has used up its fast write cache drains them slowly, so the fsync
// waits for seconds, and a leader whose fsync waits that long drops
// out of its control plane. With smaller requests, the kernel's
// writeback throttle keeps the queue short and the fsync waits less.
// Milestone 100 in plans/completed/ has the measurements.
//
// init applies the settings at boot, before k3s starts, and
// machine-operator applies them again on every pass, so an edit takes
// effect without a reboot (machine-operator/disksettings.go).

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// DiskSettings is one entry of spec.disks.
type DiskSettings struct {
	// Device names the disk, as spec.storage.<role>.device does: a
	// kernel name, or a /dev/disk/by-id/ or /dev/disk/by-path/ name.
	// liken resolves it on every boot and every pass, so a by-id name
	// follows the disk to another port.
	Device string `json:"device"`

	// MaxRequestSize is the largest request that the kernel sends to
	// the disk, as a binary quantity ("128Ki"), or KernelRequestSize to
	// leave the disk at the kernel's own value. When it is empty,
	// liken's default applies: ClusterStateMaxRequestSize on the disk
	// that holds clusterState, and the kernel's value on any other disk.
	MaxRequestSize string `json:"maxRequestSize,omitempty"`
}

const (
	// KernelRequestSize as a maxRequestSize keeps the kernel's own
	// value on a disk, so it turns off liken's default for the
	// clusterState disk.
	KernelRequestSize = "Kernel"

	// ClusterStateMaxRequestSize is the maxRequestSize of the disk that
	// holds clusterState when no entry of spec.disks sets one for it.
	ClusterStateMaxRequestSize = "128Ki"

	// minRequestKiB is the smallest value that the kernel accepts, one
	// BLK_MIN_SEGMENT_SIZE. The kernel refuses a smaller value with
	// EINVAL.
	minRequestKiB = 4

	// clusterStateDefaultSource names the default in messages, where an
	// entry is named by its index in spec.disks.
	clusterStateDefaultSource = "the clusterState default"
)

// The reasons of a DiskSettingProblem, which are also the reasons of
// the DiskSettingsApplied condition.
const (
	// DiskNotFound is an entry whose device names no attached disk.
	DiskNotFound = "DiskNotFound"
	// DiskAboveHardwareLimit is an entry whose maxRequestSize is larger
	// than the disk's max_hw_sectors_kb.
	DiskAboveHardwareLimit = "AboveHardwareLimit"
	// DiskSettingInvalid is an entry that admission would refuse, or one
	// that names a disk that an earlier entry already names.
	DiskSettingInvalid = "InvalidSetting"
	// DiskSettingApplyFailed is a write to sysfs that the kernel refused.
	DiskSettingApplyFailed = "ApplyFailed"
)

// DiskSettingProblem is one entry of spec.disks that liken did not
// apply, with the reason and a message for a person.
type DiskSettingProblem struct {
	Reason  string
	Message string
}

// RequestSizeTarget is the max_sectors_kb that liken writes to one
// disk. KiB is 0 for an entry that sets KernelRequestSize, because a
// write of 0 makes the kernel compute its own value again. Source names
// the entry or the default that asked for the value.
type RequestSizeTarget struct {
	Disk   string
	KiB    uint64
	Source string
}

// PlanRequestSizes resolves spec.disks against the attached disks and
// returns the value to write to each disk, in the order of the entries
// and then the clusterState default. clusterStateDisk is the kernel
// name of the disk that holds clusterState, or "" when clusterState is
// on the RAM root.
//
// An entry that liken cannot use is left out and reported as a
// problem, and its disk keeps liken's default. The CRD refuses most
// such entries, but a manifest on an install stick never passes
// admission, and admission cannot know a disk's hardware limit.
func PlanRequestSizes(entries []DiskSettings, disks []BlockDevice, clusterStateDisk string) ([]RequestSizeTarget, []DiskSettingProblem) {
	var targets []RequestSizeTarget
	var problems []DiskSettingProblem
	refuse := func(reason, format string, args ...any) {
		problems = append(problems, DiskSettingProblem{Reason: reason, Message: fmt.Sprintf(format, args...)})
	}
	named := map[string]int{}
	set := map[string]bool{}
	for i, entry := range entries {
		field := fmt.Sprintf("spec.disks[%d]", i)
		if err := checkDiskDevice(entry.Device); err != nil {
			refuse(DiskSettingInvalid, "%s: %v", field, err)
			continue
		}
		matches := disksNamed(entry.Device, disks)
		switch len(matches) {
		case 0:
			refuse(DiskNotFound, "%s: no attached disk is %s", field, entry.Device)
			continue
		case 1:
		default:
			refuse(DiskSettingInvalid, "%s: %s names %d disks (%s), so liken sets none of them",
				field, entry.Device, len(matches), strings.Join(diskNames(matches), ", "))
			continue
		}
		disk := matches[0]
		if first, ok := named[disk.Name]; ok {
			refuse(DiskSettingInvalid, "%s and spec.disks[%d] both name the disk %s, so liken uses only spec.disks[%d]",
				field, first, disk.Name, first)
			continue
		}
		named[disk.Name] = i
		if entry.MaxRequestSize == "" {
			continue
		}
		kib, err := parseRequestSize(entry.MaxRequestSize)
		if err != nil {
			refuse(DiskSettingInvalid, "%s: %v", field, err)
			continue
		}
		// Older kernels refuse a value above the hardware limit with
		// EINVAL, and current ones lower it to the limit without an
		// error. liken refuses it on every kernel, so the person who
		// asked for it learns that the disk cannot take it.
		if hardware := requestSizeKiB(disk.MaxHardwareRequestSize); kib != 0 && hardware != 0 && kib > hardware {
			refuse(DiskAboveHardwareLimit, "%s: %s accepts requests of at most %s, so it cannot take maxRequestSize %s",
				field, disk.Name, disk.MaxHardwareRequestSize, entry.MaxRequestSize)
			continue
		}
		set[disk.Name] = true
		targets = append(targets, RequestSizeTarget{Disk: disk.Name, KiB: kib, Source: field})
	}
	if clusterStateDisk != "" && !set[clusterStateDisk] {
		kib, _ := parseRequestSize(ClusterStateMaxRequestSize)
		for _, d := range disks {
			if hardware := requestSizeKiB(d.MaxHardwareRequestSize); d.Name == clusterStateDisk && hardware != 0 {
				kib = min(kib, hardware)
			}
		}
		targets = append(targets, RequestSizeTarget{Disk: clusterStateDisk, KiB: kib, Source: clusterStateDefaultSource})
	}
	return targets, problems
}

// checkDiskDevice applies the rules of spec.storage.<role>.device to a
// device name. A by-uuid name names a filesystem and not a disk.
func checkDiskDevice(device string) error {
	if !strings.HasPrefix(device, "/dev/") {
		return fmt.Errorf("device %q is not a /dev name", device)
	}
	suffix, ok := strings.CutPrefix(device, "/dev/disk/")
	if !ok {
		return nil
	}
	tree, name, _ := strings.Cut(suffix, "/")
	if tree != "by-id" && tree != "by-path" {
		return fmt.Errorf("device %s is in /dev/disk/%s, and a disk is named by a /dev name or a name in /dev/disk/by-id or /dev/disk/by-path", device, tree)
	}
	if name == "" {
		return fmt.Errorf("device %s is the /dev/disk/%s directory, not a disk in it", device, tree)
	}
	return nil
}

// disksNamed returns the disks that device names: the disk whose
// kernel name it is, or each disk with it among its stable names.
func disksNamed(device string, disks []BlockDevice) []BlockDevice {
	var matches []BlockDevice
	for _, d := range disks {
		if "/dev/"+d.Name == device || slices.Contains(d.StableNames, device) {
			matches = append(matches, d)
		}
	}
	return matches
}

func diskNames(disks []BlockDevice) []string {
	var names []string
	for _, d := range disks {
		names = append(names, d.Name)
	}
	return names
}

// parseRequestSize returns a maxRequestSize in KiB, or 0 for
// KernelRequestSize. The kernel takes whole KiB, so a value that is not
// one is refused rather than rounded.
func parseRequestSize(s string) (uint64, error) {
	if s == KernelRequestSize {
		return 0, nil
	}
	bytes, err := ParseSize(s)
	if err != nil {
		return 0, fmt.Errorf("maxRequestSize %q is not %s or a binary quantity such as %s", s, KernelRequestSize, ClusterStateMaxRequestSize)
	}
	if bytes%1024 != 0 {
		return 0, fmt.Errorf("maxRequestSize %q is not a whole number of KiB, and the kernel sets the size in KiB", s)
	}
	if bytes>>10 < minRequestKiB {
		return 0, fmt.Errorf("maxRequestSize %q is smaller than %s, the smallest size that the kernel accepts", s, RequestSizeText(minRequestKiB))
	}
	return bytes >> 10, nil
}

// requestSizeKiB reads back a size that RequestSizeText wrote, and
// returns 0 for an empty or unreadable one.
func requestSizeKiB(s string) uint64 {
	bytes, err := ParseSize(s)
	if err != nil {
		return 0
	}
	return bytes >> 10
}

// RequestSizeText writes a size in KiB in the syntax of maxRequestSize,
// so a status value can be pasted into spec.disks.
func RequestSizeText(kib uint64) string {
	return SizeText(kib << 10)
}

// DiskOfPartition returns the disk among disks that holds a partition,
// such as sda for sda5 or nvme0n1 for nvme0n1p5. sysBlock is
// /sys/block, where the kernel lists each partition as a directory of
// its disk. It returns "" when no disk holds the partition.
func DiskOfPartition(sysBlock, partition string, disks []BlockDevice) string {
	if partition == "" {
		return ""
	}
	for _, d := range disks {
		if info, err := os.Stat(filepath.Join(sysBlock, d.Name, partition)); err == nil && info.IsDir() {
			return d.Name
		}
	}
	return ""
}

// ReadRequestLimits returns a disk's max_sectors_kb and
// max_hw_sectors_kb.
func ReadRequestLimits(sysBlock, disk string) (current, hardware uint64, err error) {
	if current, err = readQueueKiB(sysBlock, disk, "max_sectors_kb"); err != nil {
		return 0, 0, err
	}
	if hardware, err = readQueueKiB(sysBlock, disk, "max_hw_sectors_kb"); err != nil {
		return 0, 0, err
	}
	return current, hardware, nil
}

func readQueueKiB(sysBlock, disk, attribute string) (uint64, error) {
	raw, err := os.ReadFile(filepath.Join(sysBlock, disk, "queue", attribute))
	if err != nil {
		return 0, fmt.Errorf("reading %s of %s: %w", attribute, disk, err)
	}
	kib, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading %s of %s: %w", attribute, disk, err)
	}
	return kib, nil
}

// WriteMaxRequestSize writes a disk's max_sectors_kb. It does not create
// the file, so a disk that went away returns an error.
func WriteMaxRequestSize(sysBlock, disk string, kib uint64) error {
	path := filepath.Join(sysBlock, disk, "queue", "max_sectors_kb")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("opening max_sectors_kb of %s for writing: %w", disk, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, kib); err != nil {
		return fmt.Errorf("writing %d to max_sectors_kb of %s: %w", kib, disk, err)
	}
	return nil
}

// FillRequestLimits sets the request sizes of a BlockDevice from sysfs.
// It leaves both empty when sysfs has no queue for the disk.
func FillRequestLimits(sysBlock string, d *BlockDevice) {
	current, hardware, err := ReadRequestLimits(sysBlock, d.Name)
	if err != nil {
		d.MaxRequestSize, d.MaxHardwareRequestSize = "", ""
		return
	}
	d.MaxRequestSize, d.MaxHardwareRequestSize = RequestSizeText(current), RequestSizeText(hardware)
}
