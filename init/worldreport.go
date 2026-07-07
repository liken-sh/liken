package main

// The essential mounts and the world report: the first things init
// sets up, and the way it shows its work.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// The essential filesystems are declared as a table rather than a
// sequence of calls: they are data, and the world report below prints
// what actually got mounted, so the two are easy to compare.
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

	reportFirmware()

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
