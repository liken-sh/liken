package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
)

// consoleOn writes the kernel's list of active consoles into a sysfs
// root.
func consoleOn(t *testing.T, sysRoot, active string) {
	t.Helper()
	dir := filepath.Join(sysRoot, "class", "tty", "console")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active"), []byte(active+"\n"), 0o644); err != nil {
		t.Fatalf("writing the active consoles: %v", err)
	}
}

// A serial controller with the console on one port still offers its
// other port, and the RTC is never offered.
func TestReservedNodesLeaveTheDelivery(t *testing.T) {
	sysRoot := t.TempDir()
	consoleOn(t, sysRoot, "ttyS0 tty0")
	serial := hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyS0", Subsystem: "tty"},
		{Path: "/dev/ttyS1", Subsystem: "tty"},
		{Path: "/dev/rtc0", Subsystem: "rtc"},
	}}

	got := withoutReserved(serial, reservedNodes(sysRoot)).DevNodes()

	if want := []string{"/dev/ttyS1"}; !slices.Equal(got, want) {
		t.Errorf("delivery = %q, want %q", got, want)
	}
}

// A machine whose kernel lists no console still keeps its RTC.
func TestHeldNodesWithoutAConsoleList(t *testing.T) {
	reserved := reservedNodes(t.TempDir())

	if !reserved[machineClock] || len(reserved) != 1 {
		t.Errorf("the reserved nodes are %v, want the clock alone", reserved)
	}
}

// A device whose only node is the RTC has nothing left to deliver, so
// the inventory does not publish it.
func TestInventoryWithholdsADeviceTheMachineReserves(t *testing.T) {
	sysRoot := t.TempDir()
	consoleOn(t, sysRoot, "ttyS0")
	reserved := reservedNodes(sysRoot)
	clock := hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/rtc0", Subsystem: "rtc"}}}

	devices := inventoryDevices([]hardware.Device{
		{Bus: "platform", Address: "rtc_cmos", Driver: "rtc_cmos"},
	}, func(hardware.Device) hardware.Delivery { return withoutReserved(clock, reserved) }, noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: init writes the clock", devices)
	}
}

// The inventory never publishes a TPM (reserved.go gives the reason).
func TestInventoryWithholdsTheTPM(t *testing.T) {
	reserved := reservedNodes(t.TempDir())
	tpm := hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/tpm0", Subsystem: "tpm"},
		{Path: "/dev/tpmrm0", Subsystem: "tpmrm"},
	}}

	devices := inventoryDevices([]hardware.Device{
		{Bus: "platform", Address: "MSFT0101:00", Driver: "tpm_crb_acpi"},
	}, func(hardware.Device) hardware.Delivery { return withoutReserved(tpm, reserved) }, noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: the TPM is the machine's", devices)
	}
}
