package main

// The proving lifecycle's decision points, exercised against a
// tempdir store and a fake efivarfs: trial detection, the fallback
// verdict, BootNext arming, and BootOrder repair.

import (
	"os"
	"testing"

	"github.com/chrisguidry/liken/machine"
)

// slotFirmware is a fake efivarfs carrying both slots' entries and a
// BootOrder, the way an installed machine's firmware looks.
func slotFirmware(t *testing.T, order ...uint16) string {
	t.Helper()
	return fakeEFIVars(t, map[string][]byte{
		"Boot0002":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"Boot0003":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot B"}),
		"BootOrder": u16le(order...),
	})
}

func stagedRelease(t *testing.T, root, version, slot string) ([]byte, string) {
	t.Helper()
	raw, hash, err := machine.RenderSystemRelease(version, slot, "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SystemReleases(root).WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	return raw, hash
}

func readBootOrder(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := readEFIVar(dir, "BootOrder")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestABootOnTheStagedSlotIsTheTrial(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if !settleSystemRelease(root, "B", true, &boot) {
		t.Error("booting the staged slot is the proving boot")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the trial itself must not consume the record; promotion is the operator's")
	}
}

func TestABootBackOnTheProvenSlotAfterATrialRejects(t *testing.T) {
	root := t.TempDir()
	raw, _ := stagedRelease(t, root, "0.2.0", "B")
	store := machine.SystemReleases(root)
	if err := store.WriteAttempted(machine.ManifestHash(raw)); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "A", true, &boot) {
		t.Error("the fallback boot is not a proving boot")
	}
	if boot.SystemRejection == nil {
		t.Fatal("falling back is the rejection verdict")
	}
	if boot.SystemRejection.Hash != machine.ManifestHash(raw) {
		t.Error("the rejection must name the exact record that fell back")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("a rejected record must leave the staged slot empty")
	}
}

func TestAStagedReleaseAwaitingItsRebootIsLeftStanding(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "A", true, &boot) {
		t.Error("no attempted marker means the trial hasn't run")
	}
	if boot.SystemRejection != nil {
		t.Error("nothing fell back; nothing to reject")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record stays staged until its reboot")
	}
}

// provenRelease records a proven release standing on a slot, the way
// promotion (or the first reconcile's seed) leaves the store.
func provenRelease(t *testing.T, root, version, slot string) {
	t.Helper()
	raw, _, err := machine.RenderSystemRelease(version, slot, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SystemReleases(root).WriteProven(raw); err != nil {
		t.Fatal(err)
	}
}

func TestArmProvingBootWritesTheMarkerThenBootNext(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "A")
	raw, _ := stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(dir, root, "A")

	attempted, _ := machine.SystemReleases(root).LoadAttempted()
	if attempted != machine.ManifestHash(raw) {
		t.Error("the attempted marker must name the staged record")
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || len(next) != 2 || next[0] != 0x03 || next[1] != 0x00 {
		t.Errorf("BootNext should aim at slot B's entry: %v, %v", next, err)
	}
}

func TestArmProvingBootRefusesWithoutAVerifiedFallback(t *testing.T) {
	root := t.TempDir()
	// No proven record at all: the fallback can't be verified, so the
	// trial must be refused — a trial without a fallback is a machine
	// bricked by its own upgrade.
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(dir, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("no fallback, no trial")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("BootNext must not be armed: %v", err)
	}
}

func TestArmProvingBootAssertsTheFallbackFirst(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	stagedRelease(t, root, "0.3.0", "A")
	// The firmware still prefers slot A from install time: arming a
	// trial of slot A must first flip the fallback to proven slot B.
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(dir, root, "B")

	if got := readBootOrder(t, dir); string(got) != string(u16le(0x0003, 0x0002)) {
		t.Errorf("the fallback must lead with the proven slot before the trial arms: % x", got)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || len(next) != 2 || next[0] != 0x02 {
		t.Errorf("the trial arms at slot A's entry once the fallback holds: %v, %v", next, err)
	}
}

func TestArmProvingBootRefusesWithoutAnEntry(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{}) // no entries at all

	armProvingBoot(dir, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("no entry, no trial: the marker must not claim one happened")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("BootNext must not be armed: %v", err)
	}
}

func underlying(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

func TestRepairBootOrderAssertsTheProvenSlot(t *testing.T) {
	root := t.TempDir()
	raw, _, err := machine.RenderSystemRelease("0.2.0", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SystemReleases(root).WriteProven(raw); err != nil {
		t.Fatal(err)
	}
	// The firmware still remembers the install's order: A first.
	dir := slotFirmware(t, 0x0002, 0x0003, 0x0000)

	repairBootOrder(dir, root)

	if got := readBootOrder(t, dir); string(got) != string(u16le(0x0003, 0x0002, 0x0000)) {
		t.Errorf("slot B should lead, everything else in order: % x", got)
	}

	// Idempotent: a second repair writes nothing new and keeps the order.
	repairBootOrder(dir, root)
	if got := readBootOrder(t, dir); string(got) != string(u16le(0x0003, 0x0002, 0x0000)) {
		t.Errorf("repair must be idempotent: % x", got)
	}
}

func TestRepairBootOrderDoesNothingWithoutAProvenRecord(t *testing.T) {
	dir := slotFirmware(t, 0x0002, 0x0003)
	repairBootOrder(dir, t.TempDir())
	if got := readBootOrder(t, dir); string(got) != string(u16le(0x0002, 0x0003)) {
		t.Errorf("no record, no opinion: % x", got)
	}
}

func TestSettleSystemReleaseIgnoresExternalMediaBoots(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "", true, &boot) {
		t.Error("external media can't prove a slot")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record must survive for a from-disk boot to judge")
	}
}
