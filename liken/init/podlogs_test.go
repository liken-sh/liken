package main

// These tests replace mountFilesystem and record each bind, because a
// test process cannot mount. The real mount and its unmount at reboot
// are tested by hand on the dev cluster.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// onPartition returns the storage status of a machine with every role
// on a partition.
func onPartition() machine.StorageStatus {
	status := machine.AllRolesInMemory()
	for _, name := range machine.StorageRoleNames {
		status.Role(name).Backing = machine.BackingPartition
	}
	return status
}

func TestPodLogsBindOntoThePodEphemeralFilesystem(t *testing.T) {
	bind := planPodLogBind(onPartition())
	if bind == nil {
		t.Fatal("planPodLogBind returned nil, want a bind because podEphemeral is on a partition")
	}
	if bind.target != "/var/log/pods" {
		t.Errorf("the bind target is %s, want /var/log/pods", bind.target)
	}
	kubeletRoot := roleMounts[machine.PodEphemeralRole].path
	if !strings.HasPrefix(bind.source, kubeletRoot+"/") {
		t.Errorf("the bind source is %s, want a path under %s/", bind.source, kubeletRoot)
	}
}

func TestPodLogsStayOnTheRootWithoutPodEphemeral(t *testing.T) {
	status := onPartition()
	status.PodEphemeral.Backing = machine.BackingMemory
	if bind := planPodLogBind(status); bind != nil {
		t.Errorf("the bind is %+v, want nil without a podEphemeral partition", bind)
	}
}

func TestPodLogsFollowThePodEphemeralMountPoint(t *testing.T) {
	dir := t.TempDir()
	old := roleMounts[machine.PodEphemeralRole]
	rm := old
	rm.path = dir
	roleMounts[machine.PodEphemeralRole] = rm
	t.Cleanup(func() { roleMounts[machine.PodEphemeralRole] = old })

	bind := planPodLogBind(onPartition())
	if bind == nil {
		t.Fatal("planPodLogBind returned nil, want a bind because the role is on a partition")
	}
	if bind.source != filepath.Join(dir, podLogsSubdir) {
		t.Errorf("the bind source is %s, want %s", bind.source, filepath.Join(dir, podLogsSubdir))
	}
}

// podLogMachine puts the podEphemeral mount path and the log path in
// one temporary directory. A path under its file `blocker` cannot be
// created.
type podLogMachine struct {
	root  string
	binds [][2]string
}

func newPodLogMachine(t *testing.T, kubeletPath, logsPath string) *podLogMachine {
	t.Helper()
	m := &podLogMachine{root: t.TempDir()}
	if err := os.WriteFile(filepath.Join(m.root, "blocker"), nil, 0o644); err != nil {
		t.Fatalf("writing the blocker file: %v", err)
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

func TestBindPodLogsBindsTheDiskDirectoryOntoTheCanonicalPath(t *testing.T) {
	m := newPodLogMachine(t, "kubelet", "var/log/pods")

	bindPodLogs(onPartition())

	source := filepath.Join(m.root, "kubelet", podLogsSubdir)
	target := filepath.Join(m.root, "var/log/pods")
	if !slices.Equal(m.binds, [][2]string{{source, target}}) {
		t.Errorf("the binds are %v, want one from %s onto %s", m.binds, source, target)
	}
	for _, dir := range []string{source, target} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s is not a directory before the bind, want one: %v", dir, err)
		}
	}
}

func TestBindPodLogsBindsNothingWithoutPodEphemeral(t *testing.T) {
	m := newPodLogMachine(t, "kubelet", "var/log/pods")
	status := onPartition()
	status.PodEphemeral.Backing = machine.BackingMemory

	bindPodLogs(status)

	if len(m.binds) != 0 {
		t.Errorf("the binds are %v, want none", m.binds)
	}
	if _, err := os.Stat(filepath.Join(m.root, "var/log/pods")); !os.IsNotExist(err) {
		t.Errorf("stat of the log path returned %v, want it absent so kubelet creates it", err)
	}
}

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
				t.Errorf("the binds are %v, want none when a directory cannot be created", m.binds)
			}
		})
	}
}
