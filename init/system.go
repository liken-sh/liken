package main

// The rest of the world k3s expects to wake up in.
//
// The essential mounts in main.go make a machine minimally alive;
// Kubernetes has a longer list of assumptions, accumulated from years
// of running on full distributions. Each function here recreates one
// of those assumptions from first principles — this file is basically
// "systemd's greatest hits, transcribed for one machine".

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

// applySysctls actuates spec.sysctls at boot. Failures are reported and
// skipped rather than fatal — a typo'd parameter shouldn't cost the
// machine its boot — and the keys are applied in sorted order so the
// console reads the same every time.
func applySysctls(sysctls map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(sysctls)) {
		value := sysctls[name]
		if err := machine.ApplySysctl(machine.SysctlDir, name, value); err != nil {
			fmt.Fprintf(os.Stderr, "liken: %v\n", err)
			continue
		}
		fmt.Printf("liken: sysctl %s = %s\n", name, value)
	}
}

// k3sMounts are the filesystems Kubernetes assumes beyond the
// essentials. Same table-driven shape as main.go's essentials.
var k3sMounts = []mount{
	// cgroup2 is how Kubernetes meters and limits every container:
	// one hierarchy under /sys/fs/cgroup where each cgroup's CPU,
	// memory, and process counts are accounted and capped. kubelet
	// flatly refuses to run without it. (Our kernel builds all the
	// controllers in; this mount is the whole setup.)
	{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /run is the conventional home for runtime state — sockets, pids,
	// locks. Everything in liken's root is RAM already, but containerd
	// and k3s hardcode paths under /run and expect it to exist.
	{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV},

	// Pseudo-terminals. kubectl exec — the no-shell OS's entire
	// interactive story — allocates its terminals here.
	{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC},

	// POSIX shared memory. Pods get a /dev/shm per-container, but the
	// runtime occasionally wants the host's to exist.
	{"tmpfs", "/dev/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV},
}

func prepareForK3s() {
	hostname, _ := os.Hostname()
	for _, m := range k3sMounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mkdir %s: %v\n", m.target, err)
			continue
		}
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mount %s on %s: %v\n", m.fstype, m.target, err)
		}
	}

	// kubelet demands that mounts made under / propagate into the
	// mount namespaces of its containers ("rshared") — it's how a
	// volume mounted after a pod starts can still appear inside it.
	// A plain root mount defaults to private; this flips the whole
	// tree, recursively.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_SHARED, ""); err != nil {
		fmt.Fprintf(os.Stderr, "liken: making / rshared: %v\n", err)
	}

	// /etc/machine-id is the systemd convention for "a stable unique
	// identifier for this installation", and enough software reads it
	// (k3s included) that a machine should have one. The kernel mints
	// UUIDs on demand at this magic path; machine-id is that, without
	// the dashes. Ours is random per boot: on a machine with no
	// writable disk, nothing persists across boots.
	if raw, err := os.ReadFile("/proc/sys/kernel/random/uuid"); err == nil {
		id := strings.NewReplacer("-", "", "\n", "").Replace(string(raw))
		if err := os.WriteFile("/etc/machine-id", []byte(id+"\n"), 0o444); err != nil {
			fmt.Fprintf(os.Stderr, "liken: machine-id: %v\n", err)
		}
	}

	// /etc/hosts: with no nsswitch and no local DNS, this file is the
	// only way "localhost" (and our own hostname) resolve. Kubernetes
	// components talk to themselves via localhost constantly.
	hosts := fmt.Sprintf("127.0.0.1 localhost\n::1 localhost\n127.0.1.1 %s\n", hostname)
	if err := os.WriteFile("/etc/hosts", []byte(hosts), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: /etc/hosts: %v\n", err)
	}

	// A node that can't forward packets can't route pod traffic; every
	// Kubernetes networking layer assumes this ancient sysctl is on.
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: ip_forward: %v\n", err)
	}

	// k3s consults $HOME and $PATH like any Unix program; PID 1 gets
	// neither from the kernel, so we invent them. The conventional
	// four directories are enough: k3s prepends its own unpacked
	// userland when it builds PATHs for the children it starts.
	os.Setenv("HOME", "/root")
	os.Setenv("PATH", "/sbin:/bin:/usr/sbin:/usr/bin")

	_ = os.MkdirAll("/root", 0o700)
	_ = os.MkdirAll("/var/log", 0o755)

	// /tmp exists on every machine — the container runtime stages
	// kubectl exec sessions there, of all things — world-writable with
	// the sticky bit, per Unix tradition. On a machine that declares
	// the systemEphemeral storage role, a disk partition is already
	// mounted here and this is a no-op; everywhere else it's RAM like
	// the rest of the root. (Chmod because MkdirAll filters modes
	// through the umask, and the sticky bit matters.)
	_ = os.MkdirAll("/tmp", 0o1777)
	_ = os.Chmod("/tmp", 0o1777)
}
