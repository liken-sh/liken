package main

// The proving lifecycle's decision points, exercised against a
// tempdir store and a fake efivarfs: trial detection, the fallback
// verdict, BootNext arming, and BootOrder repair.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liken-sh/liken/machine"
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

func TestABootOnTheStagedSlotIsTheTrial(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	trial := settleSystemRelease(root, "B", true, &boot)
	if trial == nil {
		t.Fatal("booting the staged slot is the proving boot")
	}
	if trial.Version != "0.2.0" || trial.Slot != "B" {
		t.Errorf("the trial record names the staged release: %+v", trial)
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
	if settleSystemRelease(root, "A", true, &boot) != nil {
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
	if settleSystemRelease(root, "A", true, &boot) != nil {
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
	// trial must be refused; arming it anyway could brick the machine
	// with its own upgrade.
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

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002}) {
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

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("slot B should lead, everything else in order: % x", got)
	}

	// Idempotent: a second repair writes nothing new and keeps the order.
	repairBootOrder(dir, root)
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("repair must be idempotent: % x", got)
	}
}

func TestRepairBootOrderDoesNothingWithoutAProvenRecord(t *testing.T) {
	dir := slotFirmware(t, 0x0002, 0x0003)
	repairBootOrder(dir, t.TempDir())
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("no record, no opinion: % x", got)
	}
}

func TestSettleSystemReleaseIgnoresExternalMediaBoots(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "", true, &boot) != nil {
		t.Error("external media can't prove a slot")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record must survive for a from-disk boot to judge")
	}
}

func TestRejectStagedSystemSurvivesAFailedRecord(t *testing.T) {
	// The record's write can fail (machineState remounted read-only by
	// a dying disk, say); the rejection must still be reported for
	// this boot's facts.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	store := machine.SystemReleases(filepath.Join(parent, "sealed"))
	rejection := rejectStagedDocument("system", "release", store.Reject, []byte("staged"), "the test says no")
	if rejection == nil || rejection.Reason != "the test says no" {
		t.Errorf("the rejection exists whether or not the disk took it: %+v", rejection)
	}
}

func TestAStagedReleaseWaitsForAFromDiskBoot(t *testing.T) {
	// External media is running (bootSlot is empty): the boot can't
	// judge a slot trial it isn't part of, so the record just waits.
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	boot := machine.BootStatus{}
	if settleSystemRelease(root, "", true, &boot) != nil {
		t.Error("an external-media boot is never the trial")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record stays staged for the from-disk boot")
	}
}

func TestArmProvingBootIgnoresARecordForTheRunningSlot(t *testing.T) {
	// A record staged for the very slot that is running has no trial
	// to arm: rebooting into yourself proves nothing.
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	armProvingBoot(dir, root, "B")
	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("no trial, no BootNext")
	}
}

func TestSettleSystemReleaseRejectsAGarbageStagedRecord(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	if err := store.WriteStaged([]byte("not a release record")); err != nil {
		t.Fatal(err)
	}

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "A", true, &boot) != nil {
		t.Error("garbage can never be a trial")
	}
	if boot.SystemRejection == nil {
		t.Fatal("an unparseable record is rejected on the spot")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the rejected record must leave the staged slot empty")
	}
}

func TestSettleSystemReleaseToleratesAnUnreadableStagedRecord(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	sealFile(t, filepath.Join(root, "system", "staged.yaml"))

	boot := machine.BootStatus{}
	if settleSystemRelease(root, "A", true, &boot) != nil {
		t.Error("a record that can't be read can't be a trial")
	}
}

// sealFile makes one file unreadable for the duration of a test, the
// shape of a store on a dying disk.
func sealFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

func TestRepairBootOrderToleratesAnUnreadableProvenRecord(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	sealFile(t, filepath.Join(root, "system", "proven.yaml"))
	dir := slotFirmware(t, 0x0002, 0x0003)

	repairBootOrder(dir, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("an unreadable record earns no opinion: % x", got)
	}
}

func TestRepairBootOrderToleratesAGarbageProvenRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteProven([]byte("not a release record")); err != nil {
		t.Fatal(err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	repairBootOrder(dir, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("a record that won't parse earns no opinion: % x", got)
	}
}

func TestRepairBootOrderLeavesTheOrderWhenTheSlotHasNoEntry(t *testing.T) {
	// The store says slot B is proven but the firmware has no entry
	// answering to it (a machine whose NVRAM was reset): rewriting the
	// order around a missing entry would prefer nothing.
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0002":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootOrder": u16le(0x0002),
	})

	repairBootOrder(dir, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002}) {
		t.Errorf("no entry, no rewrite: % x", got)
	}
}

func TestRepairBootOrderReportsAFailedWrite(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	// The variable refuses the write, the way flaky firmware does.
	if err := os.Chmod(filepath.Join(dir, "BootOrder-"+efiGlobalVariable), 0o444); err != nil {
		t.Fatal(err)
	}

	repairBootOrder(dir, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("a failed write leaves the firmware as it was: % x", got)
	}
}

func TestArmProvingBootIgnoresAGarbageStagedRecord(t *testing.T) {
	// Boot-time vetting owns the rejection verdict; the reboot path
	// just declines to arm anything.
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteStaged([]byte("not a release record")); err != nil {
		t.Fatal(err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(dir, root, "A")

	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("garbage must not arm a trial")
	}
}
