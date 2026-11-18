package main

// The mounts, files, and settings that k3s expects and that systemd
// sets up on a full distribution.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// sysctlDir is a variable so that a test can point applySysctls at a
// temporary directory.
var sysctlDir = machine.SysctlDir

// applySysctls writes and prints each kernel parameter in sorted
// order, so the log is the same on every boot. A parameter that fails
// to write is reported and skipped, because a mistyped parameter must
// not stop the boot.
//
// init calls it first with machine.DefaultSysctls and then with the
// Machine spec's sysctls, so the log shows the OS values and then the
// overrides.
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

// k3sMounts lists the filesystems that Kubernetes needs beyond the
// essential mounts, in the same form as the list in main.go.
var k3sMounts = []mount{
	// kubelet does not run without cgroup2. The kernel has every
	// controller built in, so the mount is all the setup it needs.
	{"cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// Pseudo-terminals, for kubectl exec -t.
	{"devpts", "/dev/pts", "devpts", unix.MS_NOSUID | unix.MS_NOEXEC},

	// POSIX shared memory. Each container gets its own /dev/shm, but
	// the container runtime also expects the host's.
	{"tmpfs", "/dev/shm", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV},
}

func prepareForK3s() {
	for _, m := range k3sMounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: creating the directory %s: %v\n", m.target, err)
			continue
		}
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mounting %s on %s: %v\n", m.fstype, m.target, err)
		}
	}

	// kubelet requires / to be rshared, so that a volume mounted
	// after a pod starts appears inside the pod's mount namespace.
	// The root mount is private by default.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_SHARED, ""); err != nil {
		fmt.Fprintf(os.Stderr, "liken: making / a shared mount: %v\n", err)
	}

	// k3s reads /etc/machine-id, which systemd writes on a full
	// distribution. The root filesystem is not kept across boots, so
	// init writes a new ID on every boot.
	if raw, err := os.ReadFile("/proc/sys/kernel/random/uuid"); err == nil {
		id := strings.NewReplacer("-", "", "\n", "").Replace(string(raw))
		if err := os.WriteFile("/etc/machine-id", []byte(id+"\n"), 0o444); err != nil {
			fmt.Fprintf(os.Stderr, "liken: writing the machine ID: %v\n", err)
		}
	}

	// PID 1 starts with no $HOME or $PATH, and k3s reads both. These
	// four directories are enough, because k3s puts the directory of
	// its own bundled tools ahead of them in the PATH of the processes
	// it starts.
	os.Setenv("HOME", "/root")
	os.Setenv("PATH", "/sbin:/bin:/usr/sbin:/usr/bin")

	_ = os.MkdirAll("/root", 0o700)
	_ = os.MkdirAll("/var/log", 0o755)

	// The container runtime writes kubectl exec sessions to /tmp. On a
	// machine with the machineEphemeral role, /tmp is that partition's
	// mount. On other machines it is part of the RAM-backed root.
	openTmp("/tmp")
}

// tmpMode is writable by every user, with the sticky bit, so a user
// can delete or rename only their own files. Go represents the bit as
// os.ModeSticky, and os ignores the 0o1000 bit of 0o1777.
const tmpMode = os.ModeSticky | 0o777

// openTmp calls Chmod because MkdirAll applies the umask and does not
// change a directory that exists.
func openTmp(dir string) {
	_ = os.MkdirAll(dir, tmpMode)
	_ = os.Chmod(dir, tmpMode)
}

func configureNameResolution(hostname string, entries []machine.HostEntry) {
	// machine.HostsFile writes the lines for localhost and the
	// hostname before spec.network.hostEntries, and a resolver uses
	// the first match, so no entry can override them.
	if err := os.WriteFile("/etc/hosts", []byte(machine.HostsFile(hostname, entries)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the hosts file: %v\n", err)
	}
	for _, entry := range entries {
		fmt.Printf("liken: /etc/hosts: %s %s\n", entry.Address, strings.Join(entry.Names, " "))
	}

	// musl ignores nsswitch.conf, and Go's resolver uses this order
	// without it. A glibc program with no nsswitch.conf queries DNS
	// before /etc/hosts, so DNS would override a host entry.
	if err := os.WriteFile("/etc/nsswitch.conf", []byte("hosts: files dns\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "liken: writing the name service switch file: %v\n", err)
	}
}
