package main

// These are tests for the one decision the pod-log bind makes: given
// what storage settled into, does this machine bind its pod logs onto
// a disk, and which directory does it bind? The decision is separable
// from the mount syscall that acts on it, so it runs here as an
// ordinary process, and a stand-in for `mountFilesystem` records the
// bind instead of making it. The mount itself, and the unmount at
// reboot, need a real machine, and belong to the QEMU harness in
// dev-cluster/.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
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

// podLogMachine points the podEphemeral mount and the canonical log
// path into one temporary directory, and records each bind instead of
// making it. The paths are relative to that directory, and a file
// named `blocker` there lets a case make a path impossible to create.
type podLogMachine struct {
	root  string
	binds [][2]string
}

func newPodLogMachine(t *testing.T, kubeletPath, logsPath string) *podLogMachine {
	t.Helper()
	m := &podLogMachine{root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(m.root, "blocker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	savedRole, savedDir, savedMount := roleMounts[machine.PodEphemeralRole], podLogsDir, mountFilesystem
	t.Cleanup(func() {
		roleMounts[machine.PodEphemeralRole], podLogsDir, mountFilesystem = savedRole, savedDir, savedMount
	})
	role := savedRole
	role.path = filepath.Join(m.root, kubeletPath)
	roleMounts[machine.PodEphemeralRole] = role
	podLogsDir = filepath.Join(m.root, logsPath)
	mountFilesystem = func(source, target, _ string, flags uintptr, _ string) error {
		if flags == unix.MS_BIND {
			m.binds = append(m.binds, [2]string{source, target})
		}
		return nil
	}
	return m
}

// A machine with podEphemeral on a disk binds a directory on that disk
// onto the canonical log path. bindPodLogs creates both directories
// first, because a bind needs an existing source and target.
func TestBindPodLogsBindsTheDiskDirectoryOntoTheCanonicalPath(t *testing.T) {
	m := newPodLogMachine(t, "kubelet", "var/log/pods")

	bindPodLogs(onPartition())

	source := filepath.Join(m.root, "kubelet", podLogsSubdir)
	target := filepath.Join(m.root, "var/log/pods")
	if !slices.Equal(m.binds, [][2]string{{source, target}}) {
		t.Errorf("one bind from podEphemeral onto the log path, got %v", m.binds)
	}
	for _, dir := range []string{source, target} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s exists as a directory before the bind: %v", dir, err)
		}
	}
}

// A machine without podEphemeral binds nothing and creates nothing at
// the log path, so kubelet writes to the root filesystem's directory.
func TestBindPodLogsBindsNothingWithoutPodEphemeral(t *testing.T) {
	m := newPodLogMachine(t, "kubelet", "var/log/pods")
	status := onPartition()
	status.PodEphemeral.Backing = machine.BackingMemory

	bindPodLogs(status)

	if len(m.binds) != 0 {
		t.Errorf("no role means no bind, got %v", m.binds)
	}
	if _, err := os.Stat(filepath.Join(m.root, "var/log/pods")); !os.IsNotExist(err) {
		t.Errorf("the log path is left for kubelet to create: %v", err)
	}
}

// A directory that cannot be created stops the bind before the mount.
// The pod logs stay on the root filesystem, and the boot continues.
func TestBindPodLogsSkipsTheBindWhenADirectoryFails(t *testing.T) {
	cases := []struct {
		name        string
		kubeletPath string
		logsPath    string
	}{
		{"the source on podEphemeral", "blocker/kubelet", "var/log/pods"},
		{"the canonical target", "kubelet", "blocker/pods"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			m := newPodLogMachine(t, one.kubeletPath, one.logsPath)

			bindPodLogs(onPartition())

			if len(m.binds) != 0 {
				t.Errorf("no bind without both directories, got %v", m.binds)
			}
		})
	}
}
