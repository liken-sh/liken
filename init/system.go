package main

// The rest of the environment that k3s expects.
//
// The essential mounts in main.go make a machine usable at a basic
// level. Kubernetes has a longer list of assumptions, built up over
// years of running on full distributions. Each function here
// recreates one of those assumptions directly: it does the part of
// systemd's setup work that this machine needs.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// osSysctls are the kernel settings that every liken machine boots
// with. This code applies them before the Machine spec's own
// sysctls, so a deployment can override any of them.
//
// watermark_scale_factor sets the size of the gap between the memory
// watermarks that start and stop kswapd, the kernel's background
// reclaimer. The value is a fraction of the machine's memory, in
// units of 0.01%. At the default gap of 0.1%, kswapd on a small
// machine runs rarely, only once allocation is close to failing.
// When that happens, many allocations stall in direct reclaim at
// once. This is the worst possible moment for a stall, because it
// produces the kind of latency spike that makes k3s's datastore miss
// its IO deadlines under load. A gap of one percent makes reclaim
// run steadily in the background instead: pages that the boot
// touched once are freed while the machine is calm, so a burst of
// activity finds free memory waiting, rather than causing a reclaim
// stall. The cost of the one percent value is a somewhat smaller
// page cache and a little more background CPU use; this cost is
// balanced against the benefit above. A gap of two percent, twice
// this value, kept kswapd visibly busy even on a well-filled
// machine, reclaiming pages that nothing needed yet.
//
// The two inotify limits raise the kernel's defaults so that init and
// the operator always have watches to spend. inotify limits are
// per-uid, and every inotify user on the machine runs as root and
// draws from the one root quota: kubelet's watches on config and
// secrets, containerd's watches, k3s's own watches, init's watch on
// the operator's intent directory, and the operator's watch on the
// facts tree. max_user_instances raises the ceiling on inotify
// instances from the kernel's 128 to 8192, the value a Kubernetes node
// commonly runs with, because kubelet and containerd alone can open
// dozens of instances and the machine plane wants two more that never
// contend with them. max_user_watches raises the ceiling on watched
// inodes to 524288 for the same reason.
//
// These numbers are caps, not allocations. An unused cap costs no
// memory. A single watch costs about a kilobyte of unswappable kernel
// memory, but only once something registers it, so raising the ceiling
// on a machine that watches little changes nothing about its memory
// use. The cap's job is to never be the thing that fails when a
// legitimate watcher asks for a watch, while it still stands as a
// backstop against a runaway watcher that would otherwise consume
// kernel memory without bound.
//
// The consequence matters more than the numbers. With this headroom
// guaranteed at boot, a watch that fails to start is a real fault, not
// an expected shortage to paper over. This is why init has no polling
// fallback for its intent watch: a failed watch surfaces on the
// console and the component plane retries it, rather than degrading to
// a poll that hides the fault.
var osSysctls = map[string]string{
	"vm.watermark_scale_factor":     "100",
	"fs.inotify.max_user_instances": "8192",
	"fs.inotify.max_user_watches":   "524288",
}

// applySysctls applies spec.sysctls at boot. If a sysctl fails,
// applySysctls reports the failure and skips it, rather than
// treating it as fatal, because a mistyped parameter should not cost
// the machine its boot. applySysctls applies the keys in sorted
// order, so the console shows the same order every time.
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

// k3sMounts lists the filesystems that Kubernetes assumes beyond the
// essential mounts. It uses the same table-driven form as the
// essentials list in main.go.
var k3sMounts = []mount{
	// Kubernetes uses cgroup2 to measure and limit every container.
	// cgroup2 is one hierarchy under /sys/fs/cgroup. Under it, the
	// kernel accounts for and caps each cgroup's CPU use, memory use,
	// and process count. kubelet does not run without this mount.
	// The kernel builds every controller in, so this mount is the
	// whole setup this step needs.
	{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /run is the standard location for runtime state: sockets,
	// PIDs, and locks. Everything under liken's root is already in
	// RAM, but containerd and k3s use hardcoded paths under /run, so
	// /run must exist.
	{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV},

	// Pseudo-terminals. kubectl exec is the only interactive access
	// on an OS with no shell, and it allocates its terminals here.
	{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC},

	// POSIX shared memory. Each pod gets its own /dev/shm per
	// container, but the container runtime sometimes needs the
	// host's /dev/shm to exist too.
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

	// kubelet requires that mounts made under / propagate into the
	// mount namespaces of its containers. This propagation mode is
	// called rshared. It lets a volume that is mounted after a pod
	// starts still appear inside the pod. A plain root mount
	// defaults to private propagation. This command changes
	// propagation for the whole tree, recursively, to shared.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_SHARED, ""); err != nil {
		fmt.Fprintf(os.Stderr, "liken: making / rshared: %v\n", err)
	}

	// /etc/machine-id is the systemd convention for a stable, unique
	// identifier for the installation. Enough software reads it,
	// including k3s, that a machine needs one. The kernel generates
	// a fresh UUID on every read of the /proc file
	// /proc/sys/kernel/random/uuid; machine-id is that UUID, without
	// the dashes. This machine's ID is random on every boot, because
	// a machine with no writable disk keeps nothing across boots.
	if raw, err := os.ReadFile("/proc/sys/kernel/random/uuid"); err == nil {
		id := strings.NewReplacer("-", "", "\n", "").Replace(string(raw))
		if err := os.WriteFile("/etc/machine-id", []byte(id+"\n"), 0o444); err != nil {
			fmt.Fprintf(os.Stderr, "liken: machine-id: %v\n", err)
		}
	}

	// /etc/hosts: this machine has no nsswitch and no local DNS, so
	// this file is the only way that "localhost", and the machine's
	// own hostname, resolve. Kubernetes components connect to
	// localhost often.
	hosts := fmt.Sprintf("127.0.0.1 localhost\n::1 localhost\n127.0.1.1 %s\n", hostname)
	if err := os.WriteFile("/etc/hosts", []byte(hosts), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: /etc/hosts: %v\n", err)
	}

	// A node that cannot forward packets cannot route pod traffic.
	// Every Kubernetes networking layer assumes that this old sysctl
	// is on.
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: ip_forward: %v\n", err)
	}

	// k3s reads $HOME and $PATH like any Unix program. PID 1 gets
	// neither variable from the kernel, so this code sets them. The
	// four conventional directories are enough, because k3s adds its
	// own unpacked userland to the front of PATH when it builds PATH
	// for the child processes it starts.
	os.Setenv("HOME", "/root")
	os.Setenv("PATH", "/sbin:/bin:/usr/sbin:/usr/bin")

	_ = os.MkdirAll("/root", 0o700)
	_ = os.MkdirAll("/var/log", 0o755)

	// /tmp exists on every machine. The container runtime stages
	// kubectl exec sessions there. By Unix convention, /tmp is
	// world-writable with the sticky bit set. On a machine that
	// declares the machineEphemeral storage role, a disk partition
	// is already mounted at /tmp, so this step does nothing there.
	// On every other machine, /tmp is RAM, like the rest of the root
	// filesystem. This code calls chmod separately because MkdirAll
	// applies the umask to the mode it is given, and the sticky bit
	// must be set exactly.
	_ = os.MkdirAll("/tmp", 0o1777)
	_ = os.Chmod("/tmp", 0o1777)
}
