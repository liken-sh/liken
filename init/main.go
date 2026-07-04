// liken — the first and only program the kernel starts.
//
// When the kernel finishes its own boot, it unpacks the initramfs into an
// in-memory root filesystem and executes one program as process ID 1. We
// name ours liken and point the kernel at it with rdinit=/liken on the
// kernel command line. That exec is the entire handoff from kernelspace:
// no environment, no arguments, no other processes, and almost no
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
// does not: its whole ambition is to make a world just barely habitable
// for k3s, start it, and keep it running — Kubernetes is the service
// manager. Today it does even less than that: it mounts the essential
// pseudo-filesystems, reports on the world it woke up in, and powers
// off. A newborn's cry, to prove the machine can live.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/unix"
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

	// Reaping starts before anything else: the moment we spawn a child
	// (or inherit an orphan), collecting its exit status is our job and
	// no one else's.
	go reap()

	mountEssentials()
	worldReport()

	// Nothing to supervise yet — a boot is complete once the report is
	// out. Powering off (never exiting! see above) hands QEMU a clean
	// shutdown, which is what lets `make run` double as a test harness.
	fmt.Println("liken: boot complete, powering off")

	// Sync flushes dirty pages to disk. Today everything lives in RAM
	// and this is a formality, but it becomes load-bearing the moment a
	// writable data partition exists, and it costs nothing to be in the
	// habit.
	unix.Sync()
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF); err != nil {
		fmt.Fprintf(os.Stderr, "liken: power off failed: %v\n", err)
	}
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
	// it's where rdinit= pointed at us, and where a future liken learns
	// things like which repo it reconciles.
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
}

// reap collects the exit status of any child process, forever. SIGCHLD
// arrives whenever a child dies; because signal coalescing can fold many
// deaths into one delivery, each wakeup drains every waiting corpse, not
// just one. (Go note: signal.Notify registers a handler with the runtime
// and forwards deliveries onto a channel, turning an async interrupt
// into an ordinary receive loop — and satisfying the "PID 1 must install
// handlers" rule from the header comment.)
func reap() {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, unix.SIGCHLD)
	for range sigchld {
		for {
			// -1 means "any child"; WNOHANG means "don't block if none
			// are dead yet" — pid 0 says the living can go on living.
			pid, err := unix.Wait4(-1, nil, unix.WNOHANG, nil)
			if pid <= 0 || err != nil {
				break
			}
		}
	}
}
