package main

// kubelet writes container logs under /var/log/pods. On a machine
// with no disk for them, they go to the root overlay, whose writable
// layer is a small fixed-size tmpfs (switchroot.go). kubelet caps each
// container's logs with containerLogMaxSize and containerLogMaxFiles,
// but not the total across containers, so busy containers can fill the
// tmpfs, and the machine has no shell to clean it up with.
//
// So init bind-mounts a directory of the podEphemeral filesystem onto
// /var/log/pods. The path stays the same because log collectors mount
// it with hostPath and the symlinks in /var/log/containers point into
// it. kubelet's podLogsDir setting could move the logs instead, but
// then every such tool would need the new path. podEphemeral is
// mounted at kubelet's root directory, which kubelet measures as
// nodefs, so its disk-pressure eviction counts the space the logs use.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// podLogsDir is a variable so that a test can use a temporary
// directory.
var podLogsDir = "/var/log/pods"

func bindPodLogDir(source, target string) error {
	return mountFilesystem(source, target, "", unix.MS_BIND, "")
}

// podLogsSubdir is relative to the podEphemeral mount. kubelet owns
// the other directories there, so the logs go under liken/.
var podLogsSubdir = filepath.Join("liken", "pod-logs")

type podLogBind struct {
	source string
	target string
}

// planPodLogBind returns nil when podEphemeral is not on a partition.
// Then /var/lib/kubelet is on the root overlay too, and a bind between
// two directories of the overlay would move no bytes off the tmpfs.
func planPodLogBind(storage machine.StorageStatus) *podLogBind {
	if storage.PodEphemeral.Backing != machine.BackingPartition {
		return nil
	}
	return &podLogBind{
		source: filepath.Join(roleMounts[machine.PodEphemeralRole].path, podLogsSubdir),
		target: podLogsDir,
	}
}

// bindPodLogs must run before k3s starts, so that kubelet opens no log
// file under the mount point. It must run after prepareForK3s makes /
// rshared, so that the bind is shared too and a collector's hostPath
// mount of /var/log/pods shows the logs on the disk.
//
// A failure is printed and the boot continues, with the logs on the
// root overlay.
func bindPodLogs(storage machine.StorageStatus) {
	bind := planPodLogBind(storage)
	if bind == nil {
		fmt.Printf("liken: pod logs: podEphemeral is not on a partition, so %s stays on the root filesystem\n", podLogsDir)
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
	if err := bindPodLogDir(bind.source, bind.target); err != nil {
		fmt.Fprintf(os.Stderr, "liken: pod logs: binding %s onto %s: %v\n", bind.source, bind.target, err)
		return
	}
	fmt.Printf("liken: pod logs: %s is %s, on podEphemeral\n", bind.target, bind.source)
}

// unmountPodLogs must run before podEphemeral is unmounted, because
// the bind keeps the filesystem busy.
func unmountPodLogs(flags int, reportErrors bool) {
	detachMount(podLogsDir, flags, reportErrors)
}
