// liken — the first and only program the kernel starts.
//
// When the kernel finishes its own boot, it unpacks the initramfs into an
// in-memory root filesystem and executes one program as process ID 1. We
// name ours liken and point the kernel at it with rdinit=/liken on the
// kernel command line. That exec is the entire handoff from kernelspace:
// a bare-bones environment, any boot parameters the kernel itself didn't
// recognize passed as arguments, no other processes, and almost no
// filesystem — just this program, alone, in a world it must finish
// building itself.
//
// PID 1 is special to the kernel in three ways, and each one shapes this
// program:
//
//   - It cannot die. If PID 1 exits for any reason, the kernel panics.
//     There is no one to fall back to; init is load-bearing by decree.
//
//   - It inherits everyone. When a process dies, its children are
//     re-parented to PID 1, which must collect ("reap") their exit
//     statuses or they linger forever as zombies in the process table.
//
//   - It is unsignalable by default. The kernel delivers a signal to
//     PID 1 only if init explicitly installed a handler for it — a
//     safety measure so a stray kill -9 can't panic the machine.
//
// A traditional init grows from here into a service manager. liken's
// does not: its whole job is to make a world just barely habitable for
// k3s, start it, and keep it running — Kubernetes is the service
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
	// descriptors 0, 1, and 2 already point somewhere real — with
	// console=ttyS0 on the kernel command line, that's the serial port.
	// Ordinary prints are all it takes to be heard.
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
	// from the new root and main starts over — which is why the console
	// says hello twice. switchroot.go tells the whole story.
	maybeSwitchRoot()

	// Reaping starts before anything else: the moment we spawn a child
	// (or inherit an orphan), collecting its exit status is our job and
	// no one else's.
	go reap()

	mountEssentials()

	// Who is this machine? The manifest is allowed to be missing (all
	// defaults) but not malformed — though even then, liken carries on
	// with defaults rather than dying: a misconfigured machine that
	// reaches the console beats a kernel panic.
	m, err := machine.Load(machine.ManifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: machine manifest: %v\n", err)
		m = &machine.Machine{}
	}
	if name := m.Metadata.Name; name != "" {
		// Sethostname is PID 1 privilege in action: one syscall, no
		// hostnamectl, no daemon. The kernel simply keeps a string.
		if err := unix.Sethostname([]byte(name)); err != nil {
			fmt.Fprintf(os.Stderr, "liken: sethostname %q: %v\n", name, err)
		} else {
			fmt.Printf("liken: i am %s\n", name)
		}
	}

	// Kernel tuning is part of who the machine is, so it happens right
	// after identity: early enough that every value holds before k3s
	// starts. The operator re-asserts the same spec once the cluster is
	// up, which is what makes a live kubectl edit stick without a
	// reboot.
	applySysctls(m.Spec.Sysctls)

	worldReport()

	conn, err := bringUpNetwork(m.Spec.Network)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: network: %v\n", err)
	} else {
		conn.report()
	}

	// If the image carries k3s, liken has its real job: build the rest
	// of the world Kubernetes expects, then supervise it forever. A
	// machine without k3s (the image's minimal form) just proves it
	// can boot and powers off.
	if _, err := os.Stat(k3sBinary); err == nil {
		prepareForK3s()
		// Facts wait until here because they live under /run, and
		// prepareForK3s just mounted a fresh tmpfs there — anything
		// written earlier would be shadowed by the mount.
		publishFacts(conn)
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

// powerOff ends the machine's life politely: sync flushes dirty pages
// to disk — on a machine whose whole world is RAM it's a formality,
// but it costs nothing and is load-bearing the moment a writable disk
// exists — and the reboot syscall does the rest. PID 1 must never
// simply exit; this is the one sanctioned way out.
func powerOff() {
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: power off failed: %v\n", err)
	}
}

// bootParam reports whether a word appears on the kernel command line
// — liken's channel for per-boot behavior that isn't machine identity
// (that belongs in the Machine manifest). Parameters are namespaced
// liken.* to stay clear of the kernel's own.
func bootParam(name string) bool {
	raw, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return false
	}
	return slices.Contains(strings.Fields(string(raw)), name)
}

// A mount table rather than a sequence of calls: the essential
// filesystems are data, and the world report below prints what actually
// got mounted, so the table and the truth can be compared at a glance.
type mount struct {
	source string
	target string
	fstype string
	flags  uintptr
}

var essentials = []mount{
	// /proc is two things at once: a directory per running process, and
	// the kernel's control panel (/proc/sys, /proc/cmdline, ...). Almost
	// any tool that inspects the system — including our own world report
	// — reads it, and k3s will refuse to start without it.
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /sys exposes the kernel's object model: every device, driver, and
	// bus, as a filesystem. cgroup2 — which Kubernetes uses to account
	// and limit every container — mounts beneath it later.
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
		// conjures its own mountpoints; the image doesn't need to ship
		// empty directories.
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mkdir %s: %v\n", m.target, err)
			continue
		}
		// Failures are reported but not fatal: a partially-built world
		// that can still print its report is worth far more to us than
		// a kernel panic, because the console is where we debug.
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mount %s on %s: %v\n", m.fstype, m.target, err)
		}
	}
}

// The world report is liken's substitute for an interactive shell: every
// question we would have answered by poking around at a prompt, init
// answers on the console, every boot. When something goes wrong, the
// fix starts with teaching the report to answer a new question.
func worldReport() {
	// Uname fills fixed-size byte arrays rather than returning strings —
	// it's a thin wrapper over the raw syscall, and the kernel ABI deals
	// in fixed-size buffers. ByteSliceToString finds the NUL terminator.
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		fmt.Printf("liken: kernel %s (%s)\n",
			unix.ByteSliceToString(u.Release[:]),
			unix.ByteSliceToString(u.Machine[:]))
	}

	// The command line is how the outside world parameterizes a boot —
	// it's where rdinit= points at us, and the natural channel for any
	// fact a machine must know before it has a filesystem.
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

	// A populated /dev is the visible proof that devtmpfs did its job
	// without any udev in the picture.
	if entries, err := os.ReadDir("/dev"); err == nil {
		fmt.Printf("liken: /dev has %d entries\n", len(entries))
	}

	reportBlockDevices()
}

// reap collects the exit status of any child process, forever. SIGCHLD
// arrives whenever a child dies; because signal coalescing can fold many
// deaths into one delivery, each wakeup drains every waiting corpse, not
// just one. This loop is the only place in liken that calls wait — every
// exit status it collects is posted to the registry in supervisor.go,
// where whoever started the process can claim it. (Go note:
// signal.Notify registers a handler with the runtime and forwards
// deliveries onto a channel, turning an async interrupt into an
// ordinary receive loop — and satisfying the "PID 1 must install
// handlers" rule from the header comment.)
func reap() {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	for range sigchld {
		for {
			// -1 means "any child"; WNOHANG means "don't block if none
			// are dead yet" — pid 0 says the living can go on living.
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
			recordDeath(pid, status)
		}
	}
}
