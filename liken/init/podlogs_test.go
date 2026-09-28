package main

// These are tests for the one decision the pod-log bind makes: given
// what storage settled into, does this machine bind its pod logs onto
// a disk, and which directory does it bind? The decision is separable
// from the mount syscall that acts on it, so it runs here as an
// ordinary process. The mount itself, and the unmount at reboot, need
// a real machine, and belong to the QEMU harness in dev-cluster/.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// onPartition builds the storage status of a machine whose declared
// roles all landed on disk, so each test can say which role it is
// about by taking one back.
func onPartition() machine.StorageStatus {
	status := machine.AllRolesInMemory()
	for _, name := range machine.StorageRoleNames {
		status.Role(name).Backing = machine.BackingPartition
	}
	return status
}

func TestPodLogsBindOntoThePodEphemeralFilesystem(t *testing.T) {
	// The whole point of the milestone: the logs leave the root
	// filesystem's small write budget for the disk that kubelet's
	// working space already uses, and they keep the canonical path.
	bind := planPodLogBind(onPartition())
	if bind == nil {
		t.Fatal("podEphemeral is on a partition, so the pod logs belong on it")
	}
	if bind.target != "/var/log/pods" {
		t.Errorf("the logs must stay at the path every collector and runbook names: %s", bind.target)
	}
	kubeletRoot := roleMounts[machine.PodEphemeralRole].path
	if !strings.HasPrefix(bind.source, kubeletRoot+"/") {
		t.Errorf("the bytes must land on podEphemeral, not at %s", bind.source)
	}
}

func TestPodLogsStayOnTheRootWithoutPodEphemeral(t *testing.T) {
	// Without the role, kubelet's root directory is on the overlay as
	// well, so a bind would move bytes from one overlay directory to
	// another and claim a separation that the machine does not have.
	status := onPartition()
	status.PodEphemeral.Backing = machine.BackingMemory
	if bind := planPodLogBind(status); bind != nil {
		t.Errorf("no podEphemeral role means no bind, not %+v", bind)
	}
}

func TestPodLogsFollowThePodEphemeralMountPoint(t *testing.T) {
	// The source path is derived from the role's own mount
	// translation, not written out a second time, so the two can never
	// disagree about where podEphemeral is.
	dir := t.TempDir()
	old := roleMounts[machine.PodEphemeralRole]
	rm := old
	rm.path = dir
	roleMounts[machine.PodEphemeralRole] = rm
	t.Cleanup(func() { roleMounts[machine.PodEphemeralRole] = old })

	bind := planPodLogBind(onPartition())
	if bind == nil {
		t.Fatal("the role is on a partition wherever it is mounted")
	}
	if bind.source != filepath.Join(dir, podLogsSubdir) {
		t.Errorf("the source follows the role's mount point: %s", bind.source)
	}
}
