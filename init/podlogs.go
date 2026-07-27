package main

// Pod logs, and the filesystem their bytes land on.
//
// kubelet writes every container's stdout and stderr to a file under
// /var/log/pods: one directory per pod, one file per container. That
// path is a convention that a great deal outside liken depends on. A
// log collector mounts it by hostPath, the symlinks in
// /var/log/containers resolve into it, and every runbook names it.
// kubelet does take a podLogsDir setting, and liken deliberately does
// not use it. A node whose logs are somewhere else is a node that
// every tool and every operator has to be taught about.
//
// So the path stays, and the filesystem under it changes.
//
// liken's root is a read-only squashfs with a small tmpfs upper layer
// for writes. switchroot.go states that budget and why it must stay
// small: the runtime's writes under / are few and fixed, and anything
// that grows with use belongs to a disk role. Pod logs are exactly the
// opposite kind of writer. They append for as long as the pods run.
// kubelet bounds one container's logs with containerLogMaxSize and
// containerLogMaxFiles, 10Mi and 5 files by default, and it bounds
// nothing about their sum. A few talkative containers fill the root's
// whole write budget, and on a machine with no shell there is no way
// to clean that up.
//
// A bind mount moves the bytes without moving the path. The
// directory that holds them lives on podEphemeral, the role that
// already holds kubelet's working space, and it appears at
// /var/log/pods. Every reader still finds the logs where it expects
// them. This also puts the logs on the filesystem that kubelet
// already measures: kubelet's nodefs is the filesystem of its root
// directory, so the space these logs consume is space that kubelet's
// own disk-pressure eviction can see and act on. liken sets no
// eviction thresholds of its own, and kubelet's defaults apply.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// podLogsDir is where kubelet writes container logs. This constant is
// the canonical Kubernetes path, and liken keeps it.
const podLogsDir = "/var/log/pods"

// podLogsSubdir is where the logs live on the podEphemeral
// filesystem. kubelet owns every other directory under its root, so
// the logs sit under a directory named for liken, which nothing can
// mistake for something kubelet manages. The k3s and containerd logs
// sit in such a directory on clusterState for the same reason
// (supervisor.go).
var podLogsSubdir = filepath.Join("liken", "pod-logs")

// podLogBind is one bind mount: a directory on a role's filesystem,
// and the canonical path it appears at.
type podLogBind struct {
	source string
	target string
}

// planPodLogBind determines whether this machine binds its pod logs
// onto a disk, and says which directory it binds.
//
// A machine that does not declare podEphemeral gets no bind. Its
// /var/lib/kubelet is on the root overlay as well, so a bind would
// carry bytes from one overlay directory to another and add a mount
// that claims a separation the machine does not have. A mount table
// that describes something untrue is worse than no mount, so this
// decision is to skip, and the caller says so on the console.
//
// The source path comes from the role's own mount translation rather
// than from a second copy of that path, so a role that moves takes
// its pod logs with it.
func planPodLogBind(storage machine.StorageStatus) *podLogBind {
	if storage.PodEphemeral.Backing != machine.BackingPartition {
		return nil
	}
	return &podLogBind{
		source: filepath.Join(roleMounts[machine.PodEphemeralRole].path, podLogsSubdir),
		target: podLogsDir,
	}
}

// bindPodLogs actuates that decision. It runs after prepareForK3s,
// which creates /var/log and makes / rshared, and before k3s starts,
// so the mount is in place before kubelet opens its first log file.
// The bind inherits the shared propagation that prepareForK3s set, so
// a collector's hostPath mount of /var/log/pods sees this filesystem
// and not the empty directory underneath it.
//
// A failure here is reported and never fatal. Pod logs on the root
// overlay are a machine with a bounded write budget for them, not a
// machine that must refuse to run.
func bindPodLogs(storage machine.StorageStatus) {
	bind := planPodLogBind(storage)
	if bind == nil {
		fmt.Printf("liken: pod logs: this machine declares no podEphemeral role, so %s stays on the root filesystem\n", podLogsDir)
		return
	}
	if err := os.MkdirAll(bind.source, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: pod logs: creating %s: %v\n", bind.source, err)
		return
	}
	if err := os.MkdirAll(bind.target, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "liken: pod logs: creating %s: %v\n", bind.target, err)
		return
	}
	if err := unix.Mount(bind.source, bind.target, "", unix.MS_BIND, ""); err != nil {
		fmt.Fprintf(os.Stderr, "liken: pod logs: binding %s onto %s: %v\n", bind.source, bind.target, err)
		return
	}
	fmt.Printf("liken: pod logs: %s is %s, on podEphemeral\n", bind.target, bind.source)
}

// unmountPodLogs detaches the bind. It runs before the roles
// themselves come down, because the bind holds a second reference to
// podEphemeral's filesystem: unmounting /var/lib/kubelet with the
// bind still in place detaches that mount point and leaves the disk
// in use. Detaching the bind first lets the role's own unmount
// release the filesystem.
//
// The caller passes the same flags and reporting choice it gives the
// role unmounts, so both shutdown paths treat this mount exactly as
// they treat the disks under it (storage.go explains the two paths).
func unmountPodLogs(flags int, reportErrors bool) {
	detachMount(podLogsDir, flags, reportErrors)
}
