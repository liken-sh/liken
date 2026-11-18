package main

// These tests run the trial boot against a store in a temporary
// directory and a fake efivarfs.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// slotFirmware returns a fake efivarfs with a boot entry for each slot
// and the given BootOrder, as an installed machine's firmware has.
func slotFirmware(t *testing.T, order ...uint16) string {
	t.Helper()
	return fakeEFIVars(t, map[string][]byte{
		"Boot0002":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"Boot0003":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot B"}),
		"BootOrder": u16le(order...),
	})
}

// stagedRelease writes a staged release record for a slot and puts
// that release on the slot, as the operator does when a download
// verifies.
func stagedRelease(t *testing.T, root, version, slot string) ([]byte, string) {
	t.Helper()
	_, digest := slotHoldingRelease(t, slot, version)
	raw, hash, err := machine.RenderSystemRelease(version, slot, digest)
	if err != nil {
		t.Fatalf("rendering the staged release record: %v", err)
	}
	if err := machine.SystemReleases(root).WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged release record: %v", err)
	}
	return raw, hash
}

func TestABootOnTheStagedSlotIsTheTrial(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	trial := settleSystemRelease(noActuator{}, root, "B", true, &boot)
	if trial == nil {
		t.Fatal("settleSystemRelease on the staged slot returned no trial, want the staged release")
	}
	if trial.Version != "0.2.0" || trial.Slot != "B" {
		t.Errorf("the trial is %+v, want release 0.2.0 on slot B", trial)
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the staged record is gone after the trial boot, want it kept until the operator promotes the release")
	}
}

func TestABootBackOnTheProvenSlotAfterATrialRejects(t *testing.T) {
	root := t.TempDir()
	raw, _ := stagedRelease(t, root, "0.2.0", "B")
	store := machine.SystemReleases(root)
	if err := store.WriteAttempted(machine.ManifestHash(raw)); err != nil {
		t.Fatalf("writing the attempted marker: %v", err)
	}

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial after the fallback boot, want none")
	}
	if boot.SystemRejection == nil {
		t.Fatal("no rejection after the trial fell back to the proven slot, want one")
	}
	if boot.SystemRejection.Hash != machine.ManifestHash(raw) {
		t.Error("the rejection has another hash than the record that fell back, want the same")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged record remains after its rejection, want none")
	}
}

func TestAStagedReleaseAwaitingItsRebootStaysStaged(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial with no attempted marker, want none")
	}
	if boot.SystemRejection != nil {
		t.Error("the boot has a rejection, want none because no trial ran")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the staged record is gone before its reboot, want it kept")
	}
}

// provenRelease writes a proven release record for a slot, as a
// promotion leaves it.
func provenRelease(t *testing.T, root, version, slot string) {
	t.Helper()
	raw, _, err := machine.RenderSystemRelease(version, slot, "")
	if err != nil {
		t.Fatalf("rendering the proven release record: %v", err)
	}
	if err := machine.SystemReleases(root).WriteProven(raw); err != nil {
		t.Fatalf("writing the proven release record: %v", err)
	}
}

func TestArmProvingBootWritesTheMarkerThenBootNext(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "A")
	raw, _ := stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "A")

	attempted, _ := machine.SystemReleases(root).LoadAttempted()
	if attempted != machine.ManifestHash(raw) {
		t.Errorf("the attempted marker is %q, want the hash of the staged record", attempted)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || len(next) != 2 || next[0] != 0x03 || next[1] != 0x00 {
		t.Errorf("BootNext is % x with error %v, want the entry of slot B", next, err)
	}
}

func TestArmProvingBootRefusesWithoutAVerifiedFallback(t *testing.T) {
	root := t.TempDir()
	// Without a proven record there is no fallback to verify.
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("the attempted marker is written with no verified fallback, want none")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("reading BootNext returned %v, want it not to exist", err)
	}
}

func TestArmProvingBootAssertsTheFallbackFirst(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	stagedRelease(t, root, "0.3.0", "A")
	// BootOrder still has slot A first, as the install wrote it.
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "B")

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002}) {
		t.Errorf("BootOrder is % x, want the proven slot first before the trial arms", got)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || len(next) != 2 || next[0] != 0x02 {
		t.Errorf("BootNext is % x with error %v, want the entry of slot A once the proven slot leads", next, err)
	}
}

// assertProven also points BootNext at the proven slot, so the reboot
// path must arm the trial after assertProven runs.
func TestTheRebootPathLeavesBootNextOnTheTrial(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "A")
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	assertAndArmForReboot(efiActuator{dir: dir}, root, "A", "")

	next, err := readEFIVar(dir, "BootNext")
	if err != nil || !slices.Equal(next, bootNextPayload(0x0003)) {
		t.Errorf("BootNext is % x with error %v, want the entry of the trial slot, not the proven slot", next, err)
	}
}

func TestArmProvingBootRefusesATrialOnTheProvenSlot(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "B")
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	// This boot runs slot A while the store records slot B as proven,
	// so the staged slot B passes the running-slot check.
	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("the attempted marker is written for a release staged on the proven slot, want none")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("reading BootNext returned %v, want it not to exist for a release staged on the proven slot", err)
	}
}

// The reboot path holds the firmware lock while it asserts the proven
// slot and arms the trial, so the proving watch cannot write between
// the two and replace the trial's BootNext.
func TestAssertProvenSlotUnderLockWaitsForTheFirmwareLock(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	// BootOrder has slot A first, so a call to assertProven that ran
	// would change it.
	dir := slotFirmware(t, 0x0002, 0x0003)

	firmwareWrites.Lock()
	done := make(chan struct{})
	go func() {
		assertProvenSlotUnderLock(efiActuator{dir: dir}, root)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("assertProvenSlotUnderLock returned while another writer held the lock, want it to wait")
	case <-time.After(50 * time.Millisecond):
	}
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x while the lock is held, want it unchanged", got)
	}

	firmwareWrites.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("assertProvenSlotUnderLock did not return within 5 seconds of the lock release, want it to run")
	}
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002}) {
		t.Errorf("BootOrder is % x after assertProvenSlotUnderLock returned, want the proven slot first", got)
	}
}

// After the reboot path releases the firmware lock, the proving watch
// can still run, and only shuttingDown stops it from undoing the trial.
func TestTheProvenAssertionStopsOnceTheMachineIsShuttingDown(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	shuttingDown.Store(true)
	t.Cleanup(func() { shuttingDown.Store(false) })

	assertProvenSlotUnderLock(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x while the machine shuts down, want it unchanged", got)
	}
}

// panicOf runs f and reports what it panicked with, or nil.
func panicOf(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

// panickingActuator is an actuator whose assertProven panics, as a bug
// in a firmware call would. Its other methods come from noActuator.
type panickingActuator struct{ noActuator }

func (panickingActuator) assertProven(string) { panic("the firmware went away") }

// The machine plane recovers a component that panics and restarts it,
// so a lock still held after the panic would block every later reboot.
func TestTheRebootFirmwareStepReleasesTheLockOnAPanic(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")

	if panicOf(func() { assertAndArmForReboot(panickingActuator{}, root, "A", "") }) == nil {
		t.Fatal("assertAndArmForReboot did not panic, want the panic to reach the machine plane")
	}

	if !firmwareWrites.TryLock() {
		t.Fatal("the firmware lock is held after a panic, want it released because a held lock blocks every later reboot")
	}
	firmwareWrites.Unlock()
}

func TestArmProvingBootRefusesWithoutAnEntry(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{}) // no entries at all

	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("the attempted marker is written with no boot entry, want none")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("reading BootNext returned %v, want it not to exist", err)
	}
}

func underlying(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

func TestAssertProvenSlotPutsTheProvenSlotFirst(t *testing.T) {
	root := t.TempDir()
	raw, _, err := machine.RenderSystemRelease("0.2.0", "B", "")
	if err != nil {
		t.Fatalf("rendering the proven release record: %v", err)
	}
	if err := machine.SystemReleases(root).WriteProven(raw); err != nil {
		t.Fatalf("writing the proven release record: %v", err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003, 0x0000)

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("BootOrder is % x, want slot B first and the others in their order", got)
	}

	assertProvenSlot(efiActuator{dir: dir}, root)
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("BootOrder is % x after a second assertion, want it unchanged", got)
	}
}

func TestAssertProvenSlotDoesNothingWithoutAProvenRecord(t *testing.T) {
	dir := slotFirmware(t, 0x0002, 0x0003)
	assertProvenSlot(efiActuator{dir: dir}, t.TempDir())
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x with no proven record, want it unchanged", got)
	}
}

func TestSettleSystemReleaseIgnoresExternalMediaBoots(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial on an external media boot, want none")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the staged record is gone after an external media boot, want it kept for a boot from the disk")
	}
}

func TestRejectStagedSystemSurvivesAFailedRecord(t *testing.T) {
	// Writing the rejection can fail, for example when a failing disk
	// remounts machineState read-only. This boot's facts still need it.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("making the parent directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	store := machine.SystemReleases(filepath.Join(parent, "sealed"))
	rejection := rejectStagedDocument("system", "release", store.Reject, []byte("staged"), "the test says no")
	if rejection == nil || rejection.Reason != "the test says no" {
		t.Errorf("the rejection is %+v, want one with the reason even when the store cannot write it", rejection)
	}
}

func TestAStagedReleaseWaitsForAFromDiskBoot(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial on an external media boot, want none")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the staged record is gone after an external media boot, want it kept")
	}
}

func TestArmProvingBootIgnoresARecordForTheRunningSlot(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	armProvingBoot(efiActuator{dir: dir}, root, "B")
	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("BootNext exists for a release staged on the running slot, want none")
	}
}

func TestSettleSystemReleaseRejectsAGarbageStagedRecord(t *testing.T) {
	root := t.TempDir()
	store := machine.SystemReleases(root)
	if err := store.WriteStaged([]byte("not a release record")); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial for an unparseable record, want none")
	}
	if boot.SystemRejection == nil {
		t.Fatal("no rejection for an unparseable staged record, want one")
	}
	if staged, _ := store.LoadStaged(); staged != nil {
		t.Error("the staged record remains after its rejection, want none")
	}
}

func TestSettleSystemReleaseToleratesAnUnreadableStagedRecord(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	sealFile(t, filepath.Join(root, "system", "staged.yaml"))

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("settleSystemRelease returned a trial for an unreadable record, want none")
	}
}

// sealFile makes a file unreadable until the test ends, as on a store
// whose disk is failing.
func sealFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("making the file unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

func TestAssertProvenSlotToleratesAnUnreadableProvenRecord(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	sealFile(t, filepath.Join(root, "system", "proven.yaml"))
	dir := slotFirmware(t, 0x0002, 0x0003)

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x with an unreadable proven record, want it unchanged", got)
	}
}

func TestAssertProvenSlotToleratesAGarbageProvenRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteProven([]byte("not a release record")); err != nil {
		t.Fatalf("writing the proven record: %v", err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x with an unparseable proven record, want it unchanged", got)
	}
}

func TestAssertProvenSlotLeavesTheOrderWhenTheSlotHasNoEntry(t *testing.T) {
	// The firmware has no entry for the proven slot B, as after a reset
	// of its NVRAM.
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0002":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootOrder": u16le(0x0002),
	})

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002}) {
		t.Errorf("BootOrder is % x with no entry for the proven slot, want it unchanged", got)
	}
}

func TestAssertProvenSlotReportsAFailedWrite(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	// BootOrder refuses writes, as some firmware does.
	if err := os.Chmod(filepath.Join(dir, "BootOrder-"+efiGlobalVariable), 0o444); err != nil {
		t.Fatalf("making BootOrder read-only: %v", err)
	}

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("BootOrder is % x after a refused write, want it unchanged", got)
	}
}

func TestArmProvingBootIgnoresAGarbageStagedRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteStaged([]byte("not a release record")); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("BootNext exists for an unparseable staged record, want none")
	}
}
