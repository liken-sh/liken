package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// requesterEntry is the boot entry that a requester points BootNext
// at, such as fwupd's "Linux Firmware Updater".
const requesterEntry = 0x0009

// keptAtPowerOn sets bootOrderKept, which main sets from
// observeBootOrder at power-on.
func keptAtPowerOn(t *testing.T, kept *bool) {
	t.Helper()
	old := bootOrderKept
	bootOrderKept = kept
	t.Cleanup(func() { bootOrderKept = old })
}

func ptr[T any](v T) *T { return &v }

// handoverMachine returns a UEFI machine that meets every condition of
// a handover. The store records slot A as proven, slot A has the
// fallback loader and leads BootOrder, bootOrderKept is true, and
// BootNext points at the requester's entry.
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
		t.Fatalf("writing BootNext: %v", err)
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

func TestHandoverLeavesBootNextAsTheRequesterSetIt(t *testing.T) {
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

// A handover reboot boots the requester's entry, so it cannot also be
// the trial of a staged release.
func TestHandoverArmsNoTrial(t *testing.T) {
	act, root, dir := handoverMachine(t)
	_, hash := stagedRelease(t, root, "0.2.0", "B")

	assertAndArmForReboot(act, root, "A", "node-1.firmware-bios")

	store := machine.SystemReleases(root)
	if attempted, _ := store.LoadAttempted(); attempted != "" {
		t.Errorf("the attempted marker names %s, want no trial armed", attempted)
	}
	if staged, _ := store.LoadStaged(); staged == nil || machine.ManifestHash(staged) != hash {
		t.Error("the staged release is gone or changed, want it kept staged for a later reboot")
	}
	if got := bootNextOf(t, dir); got != requesterEntry {
		t.Errorf("BootNext names %#04x, want the requester's %#04x", got, requesterEntry)
	}
}

// A refused handover is an ordinary reboot, which sets BootNext to the
// proven slot.
func TestRefusedHandoverRebootsToTheProvenSlot(t *testing.T) {
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
		{"when the proven slot has no fallback loader", func(t *testing.T, act *efiActuator, _, _ string) {
			slotA, _ := fakeSlotMounts(t)
			if err := os.Remove(defaultLoaderPath(slotA)); err != nil && !os.IsNotExist(err) {
				t.Fatalf("removing the fallback loader: %v", err)
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

func TestHandoverIsRefusedWithNoProvenSlot(t *testing.T) {
	act, _, dir := handoverMachine(t)

	assertAndArmForReboot(act, t.TempDir(), "A", "node-1.firmware-bios")

	if got := bootNextOf(t, dir); got == requesterEntry {
		t.Error("BootNext still names the requester's entry, want the handover refused, because the store has no proven slot to return to")
	}
}

func TestOnlyUEFIHandsOverBootNext(t *testing.T) {
	for _, act := range []bootActuator{noActuator{}, grubActuator{grubDir: t.TempDir()}} {
		if err := act.handOver("A"); err == nil {
			t.Errorf("%T.handOver returned no error, want one, because it has no one-shot boot entry", act)
		}
	}
}

// At power-on, before init writes anything, the proven slot leads
// BootOrder only when the firmware kept the order that the last
// shutdown wrote.
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
				t.Errorf("bootOrderKept is %v, want %v", deref(got), deref(c.want))
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

// A requester's pod writes its updater and capsule through espLink, so
// espLink must point at the proven slot's ESP after each promotion.
func TestTheProvenESPLinkFollowsTheProvenSlot(t *testing.T) {
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A\n")
	installedDisk(t)
	slotA, slotB := fakeSlotMounts(t)
	// The link's directory does not exist yet, as /run/liken does not
	// when boot creates the link.
	link := filepath.Join(t.TempDir(), "liken", "proven-esp")
	act := efiActuator{dir: fakeEFIVars(t, map[string][]byte{}), machineName: "node-1", espLink: link}

	for _, step := range []struct{ slot, want string }{{"A", slotA}, {"B", slotB}} {
		act.assertProven(step.slot)

		if got, err := os.Readlink(link); err != nil || got != step.want {
			t.Errorf("after slot %s is proven the link reads %q, %v; want %q", step.slot, got, err, step.want)
		}
	}
}

// A capsule left on the slot that is not proven would apply when that
// slot boots later, which is a firmware change that nobody requested.
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
		t.Errorf("slot B still has \\EFI\\UpdateCapsule (%v), want it deleted from the slot that is not proven", err)
	}
	if _, err := os.Stat(filepath.Join(slotA, "EFI", "UpdateCapsule", "fwupd-bios.cap")); err != nil {
		t.Errorf("the proven slot's capsule is missing: %v, want it kept, because it is the one that the firmware update staged", err)
	}
}

func writeNested(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
