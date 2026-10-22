package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// requesterEntry is the entry that a requester's BootNext names, such
// as fwupd's "Linux Firmware Updater".
const requesterEntry = 0x0009

// keptAtPowerOn sets what this boot found at power-on, as main's call
// to observeBootOrder does.
func keptAtPowerOn(t *testing.T, kept *bool) {
	t.Helper()
	old := bootOrderKept
	bootOrderKept = kept
	t.Cleanup(func() { bootOrderKept = old })
}

func ptr[T any](v T) *T { return &v }

// handoverMachine is a UEFI machine whose store calls slot A proven,
// whose slots carry their boot files and slot A the fallback loader,
// and whose firmware leads with slot A, kept it through the last
// reset, and holds the requester's entry in BootNext.
func handoverMachine(t *testing.T) (act efiActuator, root, dir string) {
	t.Helper()
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	fakeSlotMounts(t)
	root = t.TempDir()
	provenRelease(t, root, "0.1.0", "A")
	dir = fakeEFIVars(t, map[string][]byte{})
	act = efiActuator{dir: dir, machineName: "node-1"}
	act.assertProven("A")
	setBootNext(t, dir, requesterEntry)
	keptAtPowerOn(t, ptr(true))
	return act, root, dir
}

func setBootNext(t *testing.T, dir string, entry uint16) {
	t.Helper()
	if err := writeEFIVar(dir, "BootNext", bootNextPayload(entry)); err != nil {
		t.Fatal(err)
	}
}

func bootNextOf(t *testing.T, dir string) int32 {
	t.Helper()
	next, err := readEFIVar(dir, "BootNext")
	if err != nil {
		return -1
	}
	return bootNextEntry(next)
}

func TestTheHandoverLeavesTheRequestersBootNext(t *testing.T) {
	act, root, dir := handoverMachine(t)

	assertAndArmForReboot(act, root, "A", "node-1.firmware-bios")

	if got := bootNextOf(t, dir); got != requesterEntry {
		t.Errorf("BootNext names %#04x, want the requester's %#04x", got, requesterEntry)
	}
	slotA, _ := findSlotEntry(dir, "A")
	if order := readBootOrder(dir); len(order) == 0 || order[0] != slotA {
		t.Errorf("BootOrder is % x, want it led by slot A's entry, so the boot after the requester's comes back", order)
	}
}

// The handover's reboot boots the requester's entry, so it cannot be
// the trial of a staged release too. The release stays staged, and
// its own reboot arms its trial later.
func TestTheHandoverArmsNoTrial(t *testing.T) {
	act, root, dir := handoverMachine(t)
	_, hash := stagedRelease(t, root, "0.2.0", "B")

	assertAndArmForReboot(act, root, "A", "node-1.firmware-bios")

	store := machine.SystemReleases(root)
	if attempted, _ := store.LoadAttempted(); attempted != "" {
		t.Errorf("the attempted marker names %s, want no trial armed", attempted)
	}
	if staged, _ := store.LoadStaged(); staged == nil || machine.ManifestHash(staged) != hash {
		t.Error("the staged release must stay staged for a later reboot")
	}
	if got := bootNextOf(t, dir); got != requesterEntry {
		t.Errorf("BootNext names %#04x, want the requester's %#04x", got, requesterEntry)
	}
}

// A refused handover is an ordinary reboot: the proven slot takes the
// one shot back, and the requester's next scan finds that its update
// did not run.
func TestTheHandoverIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, act *efiActuator, root, dir string)
	}{
		{"when the last reset lost BootOrder", func(t *testing.T, _ *efiActuator, _, _ string) {
			keptAtPowerOn(t, ptr(false))
		}},
		{"when this boot could not tell", func(t *testing.T, _ *efiActuator, _, _ string) {
			keptAtPowerOn(t, nil)
		}},
		{"when BootNext names the other slot", func(t *testing.T, _ *efiActuator, _, dir string) {
			entry, _ := findSlotEntry(dir, "B")
			setBootNext(t, dir, entry)
		}},
		{"when the proven slot cannot hold the fallback loader", func(t *testing.T, act *efiActuator, _, _ string) {
			slotA, _ := fakeSlotMounts(t)
			if err := os.Remove(defaultLoaderPath(slotA)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			act.machineName = ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			act, root, dir := handoverMachine(t)
			c.setup(t, &act, root, dir)

			assertAndArmForReboot(act, root, "A", "node-1.firmware-bios")

			slotA, _ := findSlotEntry(dir, "A")
			if got := bootNextOf(t, dir); got != int32(slotA) {
				t.Errorf("BootNext names %#04x, want the proven slot's %#04x", got, slotA)
			}
		})
	}
}

func TestTheHandoverIsRefusedWithNoProvenSlot(t *testing.T) {
	act, _, dir := handoverMachine(t)

	assertAndArmForReboot(act, t.TempDir(), "A", "node-1.firmware-bios")

	if got := bootNextOf(t, dir); got == requesterEntry {
		t.Error("a store with no proven release names no slot to come back to, so the handover must be refused")
	}
}

func TestOnlyUEFIHandsOverTheOneShot(t *testing.T) {
	for _, act := range []bootActuator{noActuator{}, grubActuator{grubDir: t.TempDir()}} {
		if err := act.handOver("A"); err == nil {
			t.Errorf("%T handed over a one-shot it does not have", act)
		}
	}
}

// At power-on, before init writes anything, the proven slot leads
// BootOrder exactly when the firmware kept the order that the last
// shutdown asserted.
func TestObserveBootOrder(t *testing.T) {
	cases := []struct {
		name   string
		proven string
		order  []uint16
		want   *bool
	}{
		{"the proven slot leads", "A", []uint16{0x0002, 0x0003}, ptr(true)},
		{"the other slot leads", "B", []uint16{0x0002, 0x0003}, ptr(false)},
		{"no proven release", "", []uint16{0x0002, 0x0003}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.proven != "" {
				provenRelease(t, root, "0.1.0", c.proven)
			}
			dir := slotFirmware(t, c.order...)

			got := observeBootOrder(efiActuator{dir: dir}, root)

			if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
				t.Errorf("got %v, want %v", deref(got), deref(c.want))
			}
		})
	}
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// A requester's pod writes to the ESP that the next boot reads, which
// is the proven slot's, through one path that follows each promotion.
func TestTheProvenESPLinkFollowsTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	// The boot links the ESP before anything else creates /run/liken.
	link := filepath.Join(t.TempDir(), "liken", "proven-esp")
	act := efiActuator{dir: fakeEFIVars(t, map[string][]byte{}), machineName: "node-1", espLink: link}

	for _, step := range []struct{ slot, want string }{{"A", slotA}, {"B", slotB}} {
		act.assertProven(step.slot)

		if got, err := os.Readlink(link); err != nil || got != step.want {
			t.Errorf("after slot %s is proven the link reads %q, %v; want %q", step.slot, got, err, step.want)
		}
	}
}

// A capsule on the slot that is not proven would apply at a later boot
// from that slot, a firmware change that nobody asked for.
func TestCapsulesOnTheOtherSlotAreRemoved(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	for _, slot := range []string{slotA, slotB} {
		writeNested(t, filepath.Join(slot, "EFI", "UpdateCapsule", "fwupd-bios.cap"), []byte("capsule"))
	}
	act := efiActuator{dir: fakeEFIVars(t, map[string][]byte{}), machineName: "node-1"}

	act.assertProven("A")

	if _, err := os.Stat(filepath.Join(slotB, "EFI", "UpdateCapsule")); !os.IsNotExist(err) {
		t.Errorf("slot B still holds its capsules: %v", err)
	}
	if _, err := os.Stat(filepath.Join(slotA, "EFI", "UpdateCapsule", "fwupd-bios.cap")); err != nil {
		t.Errorf("the proven slot's capsule is the requester's, and stays: %v", err)
	}
}

func writeNested(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
