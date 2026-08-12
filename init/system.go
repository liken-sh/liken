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

// sysctlDir is the tree applySysctls writes into. It is a package
// variable rather than the constant itself, so a test can point it at
// a directory of its own instead of writing real kernel parameters on
// the machine running the test.
var sysctlDir = machine.SysctlDir

// applySysctls applies a set of kernel parameters. If one fails,
// applySysctls reports the failure and skips it, rather than treating
// it as fatal, because a mistyped parameter should not cost the
// machine its boot. It applies the keys in sorted order, so the
// console shows the same order every time.
//
// Init calls this twice: once for the settings every liken machine
// holds, in machine.OSSysctls, and once for the Machine spec's own
// sysctls. Printing each parameter on both passes is deliberate. A
// reader of the boot log sees what the OS set, and then sees which of
// those values this deployment chose to override.
func applySysctls(sysctls map[string]string) {
	for _, name := range slices.Sorted(maps.Keys(sysctls)) {
		value := sysctls[name]
		if err := machine.ApplySysctl(sysctlDir, name, value); err != nil {
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

// configureNameResolution writes /etc/hosts and /etc/nsswitch.conf, the
// two files that decide how a name on this machine resolves to an
// address.
func configureNameResolution(hostname string, entries []machine.HostEntry) {
	// /etc/hosts is the only place "localhost" and this machine's own
	// hostname resolve, and it is also where spec.network.hostEntries
	// lands: one line below the three fixed lines, for each entry the
	// manifest declares. The fixed lines come first, so a resolver's
	// first match always wins and no entry can override localhost or
	// this machine's own name.
	if err := os.WriteFile("/etc/hosts", []byte(machine.HostsFile(hostname, entries)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: /etc/hosts: %v\n", err)
	}
	for _, entry := range entries {
		fmt.Printf("liken: /etc/hosts: %s %s\n", entry.Address, strings.Join(entry.Names, " "))
	}

	// nsswitch.conf tells a glibc resolver the order to try its
	// lookup sources in. musl ignores this file, and Go's own
	// resolver already defaults to this order, so on this machine's
	// binaries today the file changes nothing. It exists in advance
	// of that: a glibc binary that lands on the node later, in a
	// vendored component or an add-on image, would otherwise query
	// DNS before it read /etc/hosts, and a static entry would lose to
	// DNS silently. Writing "files dns" makes the hosts file always
	// win, on any resolver that reads this file.
	if err := os.WriteFile("/etc/nsswitch.conf", []byte("hosts: files dns\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: /etc/nsswitch.conf: %v\n", err)
	}
}
