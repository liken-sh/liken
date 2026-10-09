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
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active"), []byte(active+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A board's serial controller with the console on one port keeps the
// other port claimable, and the clock that init writes is never
// claimable.
func TestHeldNodesLeaveTheDelivery(t *testing.T) {
	sysRoot := t.TempDir()
	consoleOn(t, sysRoot, "ttyS0 tty0")
	serial := hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyS0", Subsystem: "tty"},
		{Path: "/dev/ttyS1", Subsystem: "tty"},
		{Path: "/dev/rtc0", Subsystem: "rtc"},
	}}

	got := withoutHeld(serial, heldNodes(sysRoot)).DevNodes()

	if want := []string{"/dev/ttyS1"}; !slices.Equal(got, want) {
		t.Errorf("delivery = %q, want %q", got, want)
	}
}

// A machine whose kernel lists no console still holds its clock.
func TestHeldNodesWithoutAConsoleList(t *testing.T) {
	held := heldNodes(t.TempDir())

	if !held[machineClock] || len(held) != 1 {
		t.Errorf("held = %v, want the clock alone", held)
	}
}

// A device whose every node the machine holds has nothing left to
// deliver, so the inventory does not publish it.
func TestInventoryWithholdsADeviceTheMachineHolds(t *testing.T) {
	sysRoot := t.TempDir()
	consoleOn(t, sysRoot, "ttyS0")
	held := heldNodes(sysRoot)
	clock := hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/rtc0", Subsystem: "rtc"}}}

	devices := inventoryDevices([]hardware.Device{
		{Bus: "platform", Address: "rtc_cmos", Driver: "rtc_cmos"},
	}, func(hardware.Device) hardware.Delivery { return withoutHeld(clock, held) }, noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: init writes the clock", devices)
	}
}

// A TPM is never claimable. A pod that held its nodes could set the
// owner and lockout passwords, clear it, extend its PCRs until the next
// boot, or fill its storage: each one lasts beyond the pod and reaches
// every user of the TPM. So the inventory publishes no TPM, whatever the
// node is named.
func TestInventoryWithholdsTheTPM(t *testing.T) {
	held := heldNodes(t.TempDir())
	tpm := hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/tpm0", Subsystem: "tpm"},
		{Path: "/dev/tpmrm0", Subsystem: "tpmrm"},
	}}

	devices := inventoryDevices([]hardware.Device{
		{Bus: "platform", Address: "MSFT0101:00", Driver: "tpm_crb_acpi"},
	}, func(hardware.Device) hardware.Delivery { return withoutHeld(tpm, held) }, noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: the TPM is the machine's", devices)
	}
}
