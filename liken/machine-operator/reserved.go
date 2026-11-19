package main

// The device nodes that the machine reserves for itself, which no claim
// may receive. protection.go covers the disks. This file covers the
// console, the RTC, and the TPM.
//
// init writes boot messages to the console and the kernel writes its
// log there, so a pod with the console could read and write both. init
// writes the system clock into /dev/rtc0 each time it sets the clock,
// and the kernel lets only one process open an RTC at a time, so a pod
// with the RTC open would make that write fail.
//
// On most boards the TPM's owner and lockout hierarchies have an empty
// authorization value. An unprivileged pod with /dev/tpm0 or
// /dev/tpmrm0 could then set those values, clear the TPM, extend its
// PCRs until the next boot, or fill its storage, and each of those
// affects every later user of the TPM. The filter matches the TPM's
// nodes by kernel subsystem, so it applies whatever the node is named.
//
// The filter removes single nodes, not whole devices. A serial
// controller can have the console on one port and a UPS on another,
// and the UPS port stays available for a claim. A device with no nodes
// left is not published, like any device with nothing to deliver.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// machineClock is the RTC that init/time.go writes the system clock to.
const machineClock = "/dev/rtc0"

// reservedNodes returns the RTC and each active console. The kernel lists
// the active consoles in /sys/class/tty/console/active as tty names,
// each of which is a node under /dev.
func reservedNodes(sysRoot string) map[string]bool {
	reserved := map[string]bool{machineClock: true}
	raw, err := os.ReadFile(filepath.Join(sysRoot, "class", "tty", "console", "active"))
	if err != nil {
		return reserved
	}
	for _, name := range strings.Fields(string(raw)) {
		reserved["/dev/"+name] = true
	}
	return reserved
}

// reservedSubsystems are the kernel subsystems of the TPM's raw device and
// of its resource manager.
var reservedSubsystems = map[string]bool{"tpm": true, "tpmrm": true}

// withoutReserved removes the console, RTC, and TPM nodes from a delivery.
func withoutReserved(delivery hardware.Delivery, reserved map[string]bool) hardware.Delivery {
	delivery.Nodes = slices.DeleteFunc(delivery.Nodes, func(n hardware.DeliveredNode) bool {
		return reserved[n.Path] || reservedSubsystems[n.Subsystem]
	})
	return delivery
}
