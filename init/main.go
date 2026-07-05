// liken: the first and only program the kernel starts.
//
// When the kernel finishes its own boot, it unpacks the initramfs into
// an in-memory root filesystem and executes one program as process ID
// 1. We name ours liken and point the kernel at it with rdinit=/liken
// on the kernel command line. That exec is the entire handoff from
// kernelspace: a bare environment, any boot parameters the kernel
// itself didn't recognize passed as arguments, no other processes, and
// almost no filesystem. Everything else, init has to set up itself.
//
// PID 1 is special to the kernel in three ways, and each one shapes
// this program:
//
//   - It cannot exit. If PID 1 exits for any reason, the kernel
//     panics. There is no fallback.
//
//   - It inherits every orphan. When a process dies, its children are
//     re-parented to PID 1, which must collect ("reap") their exit
//     statuses or they linger forever as zombies in the process table.
//
//   - It is unsignalable by default. The kernel delivers a signal to
//     PID 1 only if init explicitly installed a handler for it, a
//     safety measure so a stray kill -9 can't panic the machine.
//
// A traditional init grows from here into a service manager. liken's
// does not: its whole job is to set up the minimum environment k3s
// needs, start it, and keep it running. Kubernetes is the service
// manager. When the image carries no k3s, a boot is a self-test: mount
// the essentials, read the Machine manifest, join the network, prove
// the connection with a DNS lookup, power off.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/chrisguidry/liken/machine"
)

func main() {
	// The kernel opened /dev/console for us before the exec, so file
	// descriptors 0, 1, and 2 already point somewhere real; with
	// console=ttyS0 on the kernel command line, that's the serial
	// port. Ordinary prints reach it directly.
	fmt.Println("liken: hello from userspace")

	// Refuse to run as an ordinary process. Everything below assumes
	// the authority (and duties) of PID 1, and exercising it from a
	// shell on a development machine would try to mount filesystems at
	// real system paths.
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "liken is an init and must run as PID 1; refusing")
		os.Exit(1)
	}

	// Before any mount or any child process: trade the kernel's rootfs
	// for a real root filesystem. On success this re-execs the program
	// from the new root and main starts over, which is why the console
	// says hello twice. switchroot.go explains why and how.
	maybeSwitchRoot()

	// Reaping starts before anything else: the moment we spawn a child
	// (or inherit an orphan), collecting its exit status is our job and
	// no one else's.
	go reap()

	mountEssentials()

	// The manifest is allowed to be missing (all defaults) but not
	// malformed. Even then, liken carries on with defaults rather
	// than dying: a misconfigured machine that reaches the console
	// beats a kernel panic.
	m, err := machine.Load(machine.ManifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: machine manifest: %v\n", err)
		m = &machine.Machine{}
	}
	if name := m.Metadata.Name; name != "" {
		// Sethostname is one syscall: no hostnamectl, no daemon. The
		// kernel simply keeps a string.
		if err := unix.Sethostname([]byte(name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: sethostname %q: %v\n", name, err)
		} else {
			fmt.Printf("liken: hostname is %s\n", name)
		}
	}

	// Sysctls are applied early so every value holds before k3s
	// starts. The operator re-asserts the same spec once the cluster
	// is up, which is what makes a live kubectl edit take effect
	// without a reboot.
	applySysctls(m.Spec.Sysctls)

	// Storage before anything writes under /var, and long before k3s:
	// a filesystem can't be swapped under a running cluster. This is
	// also the one actuator allowed to stop a boot; storage.go
	// explains why an unsatisfiable storage role powers the machine
	// off.
	storage := reconcileStorage(m.Spec.Storage)

	worldReport()

	conn, err := bringUpNetwork(m.Spec.Network)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	} else {
		conn.report()
	}

	// If the image carries k3s, liken has its real job: set up the
	// rest of the environment Kubernetes expects, then supervise it
	// forever. A machine without k3s (the image's minimal form) just
	// proves it can boot and powers off.
	if _, err := os.Stat(k3sBinary); err == nil {
		prepareForK3s()
		// Facts wait until here because they live under /run, and
		// prepareForK3s just mounted a fresh tmpfs there; anything
		// written earlier would be shadowed by the mount.
		publishFacts(conn, storage)
		loadModules()
		go reportWhenReady()
		superviseK3s() // never returns
	}

	// With no k3s to supervise, a boot is complete once the report is
	// out. Powering off (never exiting! see above) hands QEMU a clean
	// shutdown, which is what lets `make run` double as a test harness.
	fmt.Println("liken: boot complete, powering off")
	powerOff()
}

// powerOff shuts the machine down cleanly: sync flushes dirty pages
// to disk (a no-op on a machine with no writable disk, but essential
// the moment one exists), then the reboot syscall powers off. PID 1
// must never simply exit; this is the only correct way for init to
// stop.
func powerOff() {
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: power off failed: %v\n", err)
	}
}

// bootParam reports whether a word appears on the kernel command
// line, liken's channel for per-boot behavior that isn't machine
// configuration (that belongs in the Machine manifest). Parameters
// are namespaced liken.* to stay clear of the kernel's own.
func bootParam(name string) bool {
	raw, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(raw)), name)
}

// A mount table rather than a sequence of calls: the essential
// filesystems are data, and the world report below prints what
// actually got mounted, so the two are easy to compare.
type mount struct {
	source string
	target string
	fstype string
	flags  uintptr
}

var essentials = []mount{
	// /proc is two things at once: a directory per running process, and
	// the kernel's control panel (/proc/sys, /proc/cmdline, ...). Almost
	// any tool that inspects the system reads it (including our own
	// world report), and k3s will refuse to start without it.
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /sys exposes the kernel's object model: every device, driver, and
	// bus, as a filesystem. cgroup2, which Kubernetes uses to account
	// and limit every container, mounts beneath it later.
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// devtmpfs is the kernel's own device catalog: mount it and a node
	// appears for every device the kernel knows, maintained by the
	// kernel itself. On a machine with known hardware this replaces the
	// entire udev apparatus.
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID},
}

func mountEssentials() {
	for _, m := range essentials {
		// The initramfs root is plain RAM and freely writable, so init
		// creates its own mountpoints; the image doesn't need to ship
		// empty directories.
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mkdir %s: %v\n", m.target, err)
			continue
		}
		// Failures are reported but not fatal: a partial environment
		// that can still print its report is worth far more than a
		// kernel panic, because the console is where we debug.
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mount %s on %s: %v\n", m.fstype, m.target, err)
		}
	}
}

// The world report is liken's substitute for an interactive shell:
// every question we would have answered by poking around at a prompt,
// init answers on the console, every boot. When something goes wrong,
// the first step is usually extending the report to answer a new
// question.
func worldReport() {
	// Uname fills fixed-size byte arrays rather than returning strings:
	// it's a thin wrapper over the raw syscall, and the kernel ABI deals
	// in fixed-size buffers. ByteSliceToString finds the NUL terminator.
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		fmt.Printf("liken: kernel %s (%s)\n",
			unix.ByteSliceToString(u.Release[:]),
			unix.ByteSliceToString(u.Machine[:]))
	}

	// The command line is how the outside world parameterizes a boot:
	// it's where rdinit= points at us, and the channel for any fact a
	// machine must know before it has a filesystem.
	if cmdline, err := os.ReadFile("/proc/cmdline"); err == nil {
		fmt.Printf("liken: cmdline: %s\n", strings.TrimSpace(string(cmdline)))
	}

	// /proc/self/mounts is the kernel's authoritative mount table; if
	// mountEssentials succeeded, its work shows up here.
	if mounts, err := os.ReadFile("/proc/self/mounts"); err == nil {
		fmt.Print("liken: mounts:\n")
		for line := range strings.SplitSeq(strings.TrimSpace(string(mounts)), "\n") {
			fmt.Printf("liken:   %s\n", line)
		}
	}

	// A populated /dev shows that devtmpfs did its job without any
	// udev in the picture.
	if entries, err := os.ReadDir("/dev"); err == nil {
		fmt.Printf("liken: /dev has %d entries\n", len(entries))
	}

	reportBlockDevices()
}

// reap collects the exit status of any child process, forever.
// SIGCHLD arrives whenever a child dies; because signal coalescing can
// fold many deaths into one delivery, each wakeup collects every
// exited child, not just one. This loop is the only place in liken
// that calls wait; every exit status it collects is posted to the
// registry in supervisor.go, where whoever started the process can
// claim it. (Go note: signal.Notify registers a handler with the
// runtime and forwards deliveries onto a channel, turning an async
// interrupt into an ordinary receive loop, and satisfying the "PID 1
// must install handlers" rule from the header comment.)
func reap() {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	for range sigchld {
		for {
			// -1 means "any child"; WNOHANG means "don't block if none
			// have exited", in which case Wait4 returns pid 0.
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
			recordDeath(pid, status)
		}
	}
}
