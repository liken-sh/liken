package main

// This file tests the proving lifecycle's decision points against a
// tempdir store and a fake efivarfs. It tests trial detection, the
// fallback verdict, BootNext arming, and BootOrder repair.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/liken-sh/liken/machine"
)

// slotFirmware is a fake efivarfs that carries both slots' entries
// and a BootOrder. This matches the way an installed machine's
// firmware looks.
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
	trial := settleSystemRelease(noActuator{}, root, "B", true, &boot)
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
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
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
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("no attempted marker means the trial hasn't run")
	}
	if boot.SystemRejection != nil {
		t.Error("nothing fell back; nothing to reject")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record stays staged until its reboot")
	}
}

// provenRelease records a proven release standing on a slot. This
// matches the state that promotion, or the seed from the first
// reconcile, leaves in the store.
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

	armProvingBoot(efiActuator{dir: dir}, root, "A")

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
	// No proven record exists. The code cannot verify the fallback,
	// so it must refuse the trial. Arming the trial anyway could
	// make the machine permanently unusable through its own
	// upgrade.
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "A")

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
	// The firmware still prefers slot A from install time. Arming a
	// trial of slot A must first change the fallback to proven slot
	// B.
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "B")

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002}) {
		t.Errorf("the fallback must lead with the proven slot before the trial arms: % x", got)
	}
	next, err := readEFIVar(dir, "BootNext")
	if err != nil || len(next) != 2 || next[0] != 0x02 {
		t.Errorf("the trial arms at slot A's entry once the fallback holds: %v, %v", next, err)
	}
}

// The reboot path asserts the proven slot and then arms the trial, in
// that order. The assertion aims BootNext at the proven slot, so this
// test holds the order that keeps a staged trial in front of it.
func TestTheRebootPathLeavesBootNextOnTheTrial(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "A")
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	assertAndArmForReboot(efiActuator{dir: dir}, root, "A")

	next, err := readEFIVar(dir, "BootNext")
	if err != nil || !slices.Equal(next, bootNextPayload(0x0003)) {
		t.Errorf("the trial takes the one shot back from the proven slot: % x, %v", next, err)
	}
}

// The slot this boot runs and the slot the store calls proven are not
// always the same one: a person can pick the other slot from the
// firmware's menu, and a firmware that cannot load the proven slot
// falls through to it. A trial of the proven slot would leave the
// machine no fallback at all.
func TestArmProvingBootRefusesATrialOnTheProvenSlot(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.1.0", "B")
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)

	// This boot runs slot A while the store calls slot B proven, so
	// the staged slot passes the running-slot check.
	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if attempted, _ := machine.SystemReleases(root).LoadAttempted(); attempted != "" {
		t.Error("the trial never armed, so no marker may claim it did")
	}
	if _, err := readEFIVar(dir, "BootNext"); !os.IsNotExist(underlying(err)) {
		t.Errorf("BootNext must not aim at the proven slot's own trial: %v", err)
	}
}

// The reboot path holds the firmware lock across its whole turn: the
// proven slot's assertion and the trial it arms after it. The proving
// watch's own assertion waits on that lock, so it cannot land between
// the two and take the one shot back from the trial.
func TestTheProvenAssertionWaitsForTheFirmwareLock(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	// The firmware still holds the install's order: slot A first. So
	// an assertion that ran would be visible in BootOrder.
	dir := slotFirmware(t, 0x0002, 0x0003)

	firmwareWrites.Lock()
	done := make(chan struct{})
	go func() {
		assertProvenSlotUnderLock(efiActuator{dir: dir}, root)
		close(done)
	}()

	select {
	case <-done:
		// Unlocking after this would panic the binary, because the
		// assertion took the lock and gave it back.
		t.Fatal("the assertion must wait while another writer holds the lock")
	case <-time.After(50 * time.Millisecond):
	}
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("nothing may reach the firmware while the lock is held: % x", got)
	}

	firmwareWrites.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the assertion must run once the lock is free")
	}
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002}) {
		t.Errorf("the proven slot leads once the assertion runs: % x", got)
	}
}

// A machine on its way down has already had its firmware turn. The
// proving watch may still be running, and once the reboot path releases
// the lock, the flag is the only thing that keeps the watch away from
// the trial that path armed.
func TestTheProvenAssertionStopsOnceTheMachineIsShuttingDown(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	shuttingDown.Store(true)
	t.Cleanup(func() { shuttingDown.Store(false) })

	assertProvenSlotUnderLock(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("the firmware keeps what the reboot path left it: % x", got)
	}
}

// panicOf runs f and reports what it panicked with, or nil.
func panicOf(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

// panickingActuator stands for a firmware call that fails the way only
// a bug fails. noActuator answers everything else.
type panickingActuator struct{ noActuator }

func (panickingActuator) assertProven(string) { panic("the firmware went away") }

// The reboot path is reachable from inside a machine-plane component,
// and the plane recovers a component that panicked and restarts it. A
// lock left held by the panicked call would block every later reboot
// before the reboot syscall, and the machine would never come back
// without a power cycle.
func TestTheRebootFirmwareStepReleasesTheLockOnAPanic(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")

	if panicOf(func() { assertAndArmForReboot(panickingActuator{}, root, "A") }) == nil {
		t.Fatal("the panic must reach the machine plane, which reports and restarts")
	}

	if !firmwareWrites.TryLock() {
		t.Fatal("a panic must not leave the firmware lock held; every later reboot would block on it")
	}
	firmwareWrites.Unlock()
}

func TestArmProvingBootRefusesWithoutAnEntry(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{}) // no entries at all

	armProvingBoot(efiActuator{dir: dir}, root, "A")

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

func TestAssertProvenSlotPutsTheProvenSlotFirst(t *testing.T) {
	root := t.TempDir()
	raw, _, err := machine.RenderSystemRelease("0.2.0", "B", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.SystemReleases(root).WriteProven(raw); err != nil {
		t.Fatal(err)
	}
	// The firmware still holds the install's order: slot A first.
	dir := slotFirmware(t, 0x0002, 0x0003, 0x0000)

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("slot B should lead, everything else in order: % x", got)
	}

	// The assertion is idempotent: a second call writes nothing new
	// and keeps the same order.
	assertProvenSlot(efiActuator{dir: dir}, root)
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0003, 0x0002, 0x0000}) {
		t.Errorf("the assertion must be idempotent: % x", got)
	}
}

func TestAssertProvenSlotDoesNothingWithoutAProvenRecord(t *testing.T) {
	dir := slotFirmware(t, 0x0002, 0x0003)
	assertProvenSlot(efiActuator{dir: dir}, t.TempDir())
	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("no record, no opinion: % x", got)
	}
}

func TestSettleSystemReleaseIgnoresExternalMediaBoots(t *testing.T) {
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")

	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "", true, &boot) != nil {
		t.Error("external media can't prove a slot")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record must survive for a from-disk boot to judge")
	}
}

func TestRejectStagedSystemSurvivesAFailedRecord(t *testing.T) {
	// The record's write can fail, for example when a dying disk
	// remounts machineState read-only. Even then, the code must
	// still report the rejection as part of this boot's facts.
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
	// External media is running, so bootSlot is empty. This boot
	// cannot judge a slot trial that it is not part of, so the
	// record just waits.
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	boot := machine.BootStatus{}
	if settleSystemRelease(noActuator{}, root, "", true, &boot) != nil {
		t.Error("an external-media boot is never the trial")
	}
	if staged, _ := machine.SystemReleases(root).LoadStaged(); staged == nil {
		t.Error("the record stays staged for the from-disk boot")
	}
}

func TestArmProvingBootIgnoresARecordForTheRunningSlot(t *testing.T) {
	// A record staged for the slot that is already running has no
	// trial to arm. Rebooting into the same slot proves nothing.
	root := t.TempDir()
	stagedRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	armProvingBoot(efiActuator{dir: dir}, root, "B")
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
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
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
	if settleSystemRelease(noActuator{}, root, "A", true, &boot) != nil {
		t.Error("a record that can't be read can't be a trial")
	}
}

// sealFile makes one file unreadable for the duration of a test.
// This matches the shape of a store on a dying disk.
func sealFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
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
		t.Errorf("an unreadable record earns no opinion: % x", got)
	}
}

func TestAssertProvenSlotToleratesAGarbageProvenRecord(t *testing.T) {
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteProven([]byte("not a release record")); err != nil {
		t.Fatal(err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("a record that won't parse earns no opinion: % x", got)
	}
}

func TestAssertProvenSlotLeavesTheOrderWhenTheSlotHasNoEntry(t *testing.T) {
	// The store says that slot B is proven, but the firmware has no
	// entry for it. This happens on a machine whose NVRAM was reset.
	// Rewriting the order around a missing entry would prefer
	// nothing.
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := fakeEFIVars(t, map[string][]byte{
		"Boot0002":  encodeLoadOption(loadOption{attributes: loadOptionActive, description: "liken slot A"}),
		"BootOrder": u16le(0x0002),
	})

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002}) {
		t.Errorf("no entry, no rewrite: % x", got)
	}
}

func TestAssertProvenSlotReportsAFailedWrite(t *testing.T) {
	root := t.TempDir()
	provenRelease(t, root, "0.2.0", "B")
	dir := slotFirmware(t, 0x0002, 0x0003)
	// The variable rejects the write. This matches the behavior of
	// flaky firmware.
	if err := os.Chmod(filepath.Join(dir, "BootOrder-"+efiGlobalVariable), 0o444); err != nil {
		t.Fatal(err)
	}

	assertProvenSlot(efiActuator{dir: dir}, root)

	if got := readBootOrder(dir); !slices.Equal(got, []uint16{0x0002, 0x0003}) {
		t.Errorf("a failed write leaves the firmware as it was: % x", got)
	}
}

func TestArmProvingBootIgnoresAGarbageStagedRecord(t *testing.T) {
	// Boot-time vetting owns the rejection verdict. The reboot path
	// does not arm anything in this case.
	root := t.TempDir()
	if err := machine.SystemReleases(root).WriteStaged([]byte("not a release record")); err != nil {
		t.Fatal(err)
	}
	dir := slotFirmware(t, 0x0002, 0x0003)

	armProvingBoot(efiActuator{dir: dir}, root, "A")

	if _, err := readEFIVar(dir, "BootNext"); err == nil {
		t.Error("garbage must not arm a trial")
	}
}
