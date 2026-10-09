package main

// The nodes the machine itself holds, which no claim may receive.
//
// The storage test in inventoryDevices keeps the machine's own disks
// out of the slice. Two more nodes belong to the machine in the same
// way, and the board walk reaches both. The console is where init
// writes the boot and where the kernel writes its messages, and a pod
// that held it could read and write over both. init writes the
// system clock into /dev/rtc0 each time the clock is set, and the
// kernel lets one process at a time open an RTC, so a pod that held it
// would make that write fail.
//
// The TPM is the machine's too. Its authorization on most boards is an
// empty password for the owner and lockout hierarchies, so a pod that
// held /dev/tpm0 or /dev/tpmrm0, with no privilege, could set those
// passwords, clear the TPM, extend its PCRs until the next boot, or fill
// its storage, and each of those reaches every later user of the TPM.
// The TPM's nodes are held by their kernel subsystem, whatever their
// numbers are.
//
// The test removes the node, not the device. A board's serial
// controller can carry the console on one port and a UPS on another,
// and the UPS port stays claimable. A device left with no node is
// not published, by the same test that skips a device with nothing
// to deliver.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/hardware"
)

// machineClock is the RTC that init writes the system clock into
// (init/time.go).
const machineClock = "/dev/rtc0"

// heldNodes reads the nodes the machine holds now. The kernel lists
// each console it writes to in /sys/class/tty/console/active, by the
// tty's name under /dev.
func heldNodes(sysRoot string) map[string]bool {
	held := map[string]bool{machineClock: true}
	raw, err := os.ReadFile(filepath.Join(sysRoot, "class", "tty", "console", "active"))
	if err != nil {
		return held
	}
	for _, name := range strings.Fields(string(raw)) {
		held["/dev/"+name] = true
	}
	return held
}

// heldSubsystems are the kernel subsystems whose every node the machine
// holds: the TPM's raw device, and its resource manager.
var heldSubsystems = map[string]bool{"tpm": true, "tpmrm": true}

// withoutHeld removes the held nodes from one delivery.
func withoutHeld(delivery hardware.Delivery, held map[string]bool) hardware.Delivery {
	delivery.Nodes = slices.DeleteFunc(delivery.Nodes, func(n hardware.DeliveredNode) bool {
		return held[n.Path] || heldSubsystems[n.Subsystem]
	})
	return delivery
}
