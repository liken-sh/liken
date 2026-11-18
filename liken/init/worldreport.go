package main

// This file makes the first mounts of the boot, and prints a report of
// the machine's state to the console.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type mount struct {
	source string
	target string
	fstype string
	flags  uintptr
}

var essentials = []mount{
	// /proc has a directory for each process and the kernel's
	// settings and information, such as /proc/sys and /proc/cmdline.
	// worldReport reads it, and k3s does not start without it.
	{"proc", "/proc", "proc", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// /sys exposes every device, driver, and bus of the kernel. The
	// cgroup2 filesystem is mounted under /sys later, and Kubernetes
	// uses it to account for and limit the resources of containers.
	{"sysfs", "/sys", "sysfs", unix.MS_NOSUID | unix.MS_NOEXEC | unix.MS_NODEV},

	// devtmpfs has a device node for each device the kernel has
	// registered, and the kernel keeps the nodes current itself. With
	// devtmpfs, a machine does not need udev.
	{"devtmpfs", "/dev", "devtmpfs", unix.MS_NOSUID},

	// /run holds runtime state such as sockets, PID files, and locks.
	// containerd and k3s have fixed paths under it, and so do
	// /run/liken/facts and wpa_supplicant's files in
	// /run/liken/wireless.
	//
	// It is mounted here and not in prepareForK3s because a wireless
	// interface comes up long before k3s starts. A tmpfs mounted over
	// /run later would hide wpa_supplicant's configuration and control
	// socket from a restart of wpa_supplicant.
	{"tmpfs", "/run", "tmpfs", unix.MS_NOSUID | unix.MS_NODEV},
}

// mountEssentials prints a failed mount and goes on. A machine with
// some mounts missing can still print the world report, but if init
// exited, the kernel would panic and the console would show no report.
func mountEssentials() {
	for _, m := range essentials {
		// The initramfs root is writable RAM, so init creates the
		// mount points and the image ships no empty directories.
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "liken: creating mount point %s: %v\n", m.target, err)
			continue
		}
		if err := unix.Mount(m.source, m.target, m.fstype, m.flags, ""); err != nil {
			fmt.Fprintf(os.Stderr, "liken: mounting %s on %s: %v\n", m.fstype, m.target, err)
		}
	}
}

// worldReport prints the kernel, firmware, command line, mounts, /dev
// entry count, and block devices to the console at every boot. The
// image has no interactive shell, so this output is what a person would
// otherwise read with commands at a shell prompt.
func worldReport() {
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		fmt.Printf("liken: kernel %s (%s)\n",
			unix.ByteSliceToString(u.Release[:]),
			unix.ByteSliceToString(u.Machine[:]))
	}

	reportFirmware()

	// The kernel command line holds rdinit=, which selects init, and
	// the other values that a machine needs before it has a
	// filesystem.
	if cmdline, err := os.ReadFile(cmdlinePath); err == nil {
		fmt.Printf("liken: cmdline: %s\n", strings.TrimSpace(string(cmdline)))
	}

	// /proc/self/mounts lists what is actually mounted, which shows
	// whether mountEssentials succeeded.
	if mounts, err := os.ReadFile("/proc/self/mounts"); err == nil {
		fmt.Print("liken: mounts:\n")
		for line := range strings.SplitSeq(strings.TrimSpace(string(mounts)), "\n") {
			fmt.Printf("liken:   %s\n", line)
		}
	}

	if entries, err := os.ReadDir("/dev"); err == nil {
		fmt.Printf("liken: /dev has %d entries\n", len(entries))
	}

	reportBlockDevices()
}
