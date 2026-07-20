package main

// The essential mounts and the world report: the first things init
// sets up, and how init reports what it did.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// This code declares the essential filesystems as a table, rather
// than as a sequence of calls. The table is data, and the world
// report below prints what actually got mounted, so a reader can
// compare the two easily.
type mount struct {
	source string
	target string
	fstype string
	flags  uintptr
}

var essentials = []mount{
	// /proc is two things at once: a directory per running process,
	// and the kernel's interface for settings and information, such
	// as /proc/sys and /proc/cmdline. Almost every tool that
	// inspects the system reads /proc, including the world report
	// below, and k3s does not start without it.
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /sys exposes the kernel's object model as a filesystem: every
	// device, driver, and bus. cgroup2 mounts beneath /sys later;
	// Kubernetes uses cgroup2 to account for and limit every
	// container.
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// devtmpfs is the kernel's own device catalog. When this mount
	// happens, a device node appears for every device the kernel
	// knows about, and the kernel maintains these nodes itself. On a
	// machine with known hardware, devtmpfs replaces the entire udev
	// system.
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID},
}

func mountEssentials() {
	for _, m := range essentials {
		// The initramfs root is plain RAM and is freely writable, so
		// init creates its own mountpoints. The image does not need
		// to ship empty directories.
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mkdir %s: %v\n", m.target, err)
			continue
		}
		// init reports failures but does not treat them as fatal. A
		// partial environment that can still print its report is far
		// more useful than a kernel panic, because the console is
		// where debugging happens.
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mount %s on %s: %v\n", m.fstype, m.target, err)
		}
	}
}

// The world report is liken's substitute for an interactive shell.
// init answers, on the console at every boot, the same questions an
// operator would otherwise answer by exploring a shell prompt. When
// something goes wrong, the usual first step is to extend the report
// to answer the new question.
func worldReport() {
	// Uname fills fixed-size byte arrays, rather than returning
	// strings, because it is a thin wrapper over the raw syscall,
	// and the kernel ABI uses fixed-size buffers. ByteSliceToString
	// finds the NUL terminator in each array.
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		fmt.Printf("liken: kernel %s (%s)\n",
			unix.ByteSliceToString(u.Release[:]),
			unix.ByteSliceToString(u.Machine[:]))
	}

	reportFirmware()

	// The kernel command line is how the outside world sets
	// parameters for a boot. It is where rdinit= points at init, and
	// it is the way to pass any fact a machine must know before it
	// has a filesystem.
	if cmdline, err := os.ReadFile(cmdlinePath); err == nil {
		fmt.Printf("liken: cmdline: %s\n", strings.TrimSpace(string(cmdline)))
	}

	// /proc/self/mounts is the kernel's authoritative mount table.
	// If mountEssentials succeeded, its results appear here.
	if mounts, err := os.ReadFile("/proc/self/mounts"); err == nil {
		fmt.Print("liken: mounts:\n")
		for line := range strings.SplitSeq(strings.TrimSpace(string(mounts)), "\n") {
			fmt.Printf("liken:   %s\n", line)
		}
	}

	// A populated /dev shows that devtmpfs created the device nodes,
	// with no udev involved.
	if entries, err := os.ReadDir("/dev"); err == nil {
		fmt.Printf("liken: /dev has %d entries\n", len(entries))
	}

	reportBlockDevices()
}
