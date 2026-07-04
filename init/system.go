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
	// neither from the kernel, so we invent them. The last two PATH
	// entries are k3s's own unpacked userland (data/current is a
	// symlink k3s maintains across versions): the netfilter tools
	// kube-proxy execs live in bin/aux, and putting them on PATH here
	// beats depending on exactly how k3s rewrites PATH between its
	// re-execs.
	os.Setenv("HOME", "/root")
	os.Setenv("PATH", "/sbin:/bin:/usr/sbin:/usr/bin"+
		":/var/lib/rancher/k3s/data/current/bin"+
		":/var/lib/rancher/k3s/data/current/bin/aux")

	// One of k3s's bundled tools defeats its own bundling: bin/aux's
	// "iptables" is a legacy-vs-nftables detection script that starts
	// #!/bin/sh, and this machine has no shell to run it with — but
	// the x bit makes PATH lookups accept it, so it must be shadowed,
	// not just supplemented. liken makes the script's decision
	// statically instead: /sbin (which k3s keeps ahead of its bundle
	// dirs when building children's PATHs) links each iptables name to
	// the legacy xtables binaries, matching the iptable_* kernel
	// modules the image ships. The links dangle until k3s first
	// unpacks itself and resolve forever after.
	_ = os.MkdirAll("/sbin", 0o755)
	aux := "/var/lib/rancher/k3s/data/current/bin/aux/"
	for _, tool := range []string{
		"iptables", "iptables-save", "iptables-restore",
		"ip6tables", "ip6tables-save", "ip6tables-restore",
	} {
		legacy := strings.Replace(tool, "tables", "tables-legacy", 1)
		if err := os.Symlink(aux+legacy, "/sbin/"+tool); err != nil && !os.IsExist(err) {
			fmt.Fprintf(os.Stderr, "liken: symlink %s: %v\n", tool, err)
		}
	}
	_ = os.MkdirAll("/root", 0o700)
	_ = os.MkdirAll("/var/log", 0o755)
}
