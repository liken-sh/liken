package main

// Each pass applies spec.disks and liken's default for the clusterState
// disk (machine/disksettings.go) to the running machine, as it applies
// sysctls. init applied the settings of the boot's manifest before k3s
// started, so a pass writes only after an edit, or after another
// program changed a disk's value.
//
// The pod is privileged, so its /sys is writable, and /sys/block lists
// the host's disks, because the kernel does not put block devices in
// namespaces. The kernel sends no event when a queue attribute
// changes, so the sysctl check in backstop.go also reads the value of
// each disk that a pass set.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

const diskSettingsAppliedCondition = "DiskSettingsApplied"

// sysBlockRoot is the host's /sys/block, under the sysfs root that the
// device inventory reads. A test of a whole pass points that root at a
// temporary directory, so the pass writes no disk of the test's host.
func sysBlockRoot() string {
	return filepath.Join(draSysfsRoot, "block")
}

// diskSettingsResult is what one pass did to the disks.
type diskSettingsResult struct {
	// unknown is true when the pass had no facts, so it could not tell
	// which disks are attached or which one holds clusterState.
	unknown  bool
	targets  []machine.RequestSizeTarget
	problems []machine.DiskSettingProblem
	// applied is the max_sectors_kb of each disk with a target, read
	// back after the pass, which the periodic check compares with.
	applied map[string]uint64
}

// requestSizeMemory records, for each disk that the operator returned
// to the kernel's value, the value that the kernel computed. A write of
// 0 restores the kernel's value, and that value depends on the disk's
// hardware and its optimal I/O size, so the operator cannot compute it
// to compare with. Without the record it would write 0 on every pass,
// and the backstop would report each write as a repair. The first pass
// after the operator starts writes 0 once for each such disk, which
// changes nothing on a disk that already has the kernel's value.
//
// Only the reconcile goroutine uses it, so it has no lock. Every
// method does nothing on a nil *requestSizeMemory.
type requestSizeMemory struct {
	reset map[string]uint64
}

func newRequestSizeMemory() *requestSizeMemory {
	return &requestSizeMemory{reset: map[string]uint64{}}
}

// keepsKernelValue reports whether the disk still reads as it did after
// the operator returned it to the kernel's value.
func (m *requestSizeMemory) keepsKernelValue(disk string, current uint64) bool {
	if m == nil {
		return false
	}
	kept, ok := m.reset[disk]
	return ok && kept == current
}

func (m *requestSizeMemory) record(disk string, target, readBack uint64) {
	if m == nil {
		return
	}
	if target == 0 {
		m.reset[disk] = readBack
		return
	}
	delete(m.reset, disk)
}

// applyDiskSettings writes max_sectors_kb on each disk that needs it,
// under sysBlock, the host's /sys/block. A disk whose value already
// matches costs one read and no write. out records each write and each
// failure.
func applyDiskSettings(sysBlock string, entries []machine.DiskSettings, facts *machine.MachineStatus, out *passOutcome, mem *requestSizeMemory) diskSettingsResult {
	if facts == nil {
		return diskSettingsResult{unknown: true}
	}
	disks := facts.Hardware.BlockDevices
	clusterStateDisk := ""
	if cs := facts.Storage.ClusterState; cs.Backing == machine.BackingPartition {
		clusterStateDisk = machine.DiskOfPartition(sysBlock, cs.Device, disks)
	}
	result := diskSettingsResult{applied: map[string]uint64{}}
	result.targets, result.problems = machine.PlanRequestSizes(entries, disks, clusterStateDisk)
	fail := func(target machine.RequestSizeTarget, step string, err error) {
		out.fail(step, err)
		result.problems = append(result.problems, machine.DiskSettingProblem{
			Reason:  machine.DiskSettingApplyFailed,
			Message: fmt.Sprintf("%s: %v", target.Source, err),
		})
	}
	for _, target := range result.targets {
		current, _, err := machine.ReadRequestLimits(sysBlock, target.Disk)
		if err != nil {
			fail(target, "reading the request size of "+target.Disk, err)
			continue
		}
		if target.KiB == 0 && mem.keepsKernelValue(target.Disk, current) || target.KiB != 0 && current == target.KiB {
			mem.record(target.Disk, target.KiB, current)
			result.applied[target.Disk] = current
			continue
		}
		step := "writing the request size of " + target.Disk
		if err := machine.WriteMaxRequestSize(sysBlock, target.Disk, target.KiB); err != nil {
			fail(target, step, err)
			continue
		}
		out.wrote(step)
		if current, _, err = machine.ReadRequestLimits(sysBlock, target.Disk); err != nil {
			fail(target, "reading the request size of "+target.Disk, err)
			continue
		}
		mem.record(target.Disk, target.KiB, current)
		result.applied[target.Disk] = current
	}
	return result
}

// diskSettingsCondition reports the result of applyDiskSettings. The
// reason of a False condition is the reason of the first problem, and
// the message lists every problem.
func diskSettingsCondition(result diskSettingsResult) api.Condition {
	condition := api.Condition{Type: diskSettingsAppliedCondition}
	switch {
	case result.unknown:
		condition.Status, condition.Reason = api.ConditionUnknown, "FactsUnreadable"
		condition.Message = "The facts that list the disks cannot be read, so no disk setting is applied"
	case len(result.problems) > 0:
		var messages []string
		for _, p := range result.problems {
			messages = append(messages, p.Message)
		}
		condition.Status, condition.Reason = api.ConditionFalse, result.problems[0].Reason
		condition.Message = strings.Join(messages, "; ")
	case len(result.targets) == 0:
		condition.Status, condition.Reason = api.ConditionTrue, "NothingToApply"
		condition.Message = "clusterState is in memory and spec.disks sets no disk, so every disk keeps the kernel's request size"
	default:
		var applied []string
		for _, target := range result.targets {
			value := machine.RequestSizeText(result.applied[target.Disk])
			if target.KiB == 0 {
				value = "the kernel's value, " + value
			}
			applied = append(applied, fmt.Sprintf("%s takes requests of at most %s, from %s", target.Disk, value, target.Source))
		}
		condition.Status, condition.Reason = api.ConditionTrue, "Applied"
		condition.Message = strings.Join(applied, "; ")
	}
	return condition
}

// requestSizesInEffect returns a copy of disks with the request sizes
// that sysfs reports now, because a pass can change them after init
// published the facts.
func requestSizesInEffect(sysBlock string, disks []machine.BlockDevice) []machine.BlockDevice {
	current := slices.Clone(disks)
	for i := range current {
		machine.FillRequestLimits(sysBlock, &current[i])
	}
	return current
}

// requestSizesDrifted reports whether a disk that the last pass set now
// reads differently, or can no longer be read.
func (c sysctlCheck) requestSizesDrifted(sysBlock string) bool {
	for disk, want := range c.requestSizes {
		current, _, err := machine.ReadRequestLimits(sysBlock, disk)
		if err != nil || current != want {
			return true
		}
	}
	return false
}
