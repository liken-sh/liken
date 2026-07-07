package main

// The proving boot: how a downloaded release becomes the running one.
//
// The operator stages a SystemRelease record (machine/systemrelease.go)
// when a verified release is waiting on the inactive slot. From
// there, this file carries the upgrade across the reboot:
//
//   - On the way down (armProvingBoot, called from init's reboot
//     path), init writes the attempted marker and arms the firmware's
//     BootNext at the staged slot's boot entry. BootNext is the
//     mechanism that makes blue-green safe: the firmware consumes it
//     as it boots, so the trial gets exactly one chance, and any
//     reset after that (a kernel panic, a watchdog, a power cut)
//     falls back to BootOrder, which still prefers the proven slot.
//
//   - On the way up (settleSystemRelease), init reads the files and
//     decides what happened. If this boot came from the staged slot,
//     this is the trial: the operator's first reconcile demonstrates
//     that the new kernel, init, k3s, and the join all work, and
//     that is what promotes the release. If this boot came from the
//     proven slot with the attempted marker set, the trial ran and
//     the firmware fell back, so the record is rejected durably, and
//     the operator will not stage that exact record again.
//
//   - After promotion (provingWatch, a machine-plane component that
//     runs only on proving boots), init rewrites BootOrder so the
//     newly proven slot leads. Init owns this write for the same
//     reason it owns every efivars write: talking to the firmware is
//     the machine plane's job, and the store on disk is the
//     authority. repairBootOrder re-asserts the proven record's slot
//     on every boot, so a power cut between promotion and the
//     BootOrder rewrite costs one extra boot of the old version,
//     never a wrong BootOrder that sticks.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// settleSystemRelease reads the system store at boot and renders the
// verdicts only init can: is this boot a trial, or the fallback from
// one? It returns true when this boot is proving a staged release,
// which is what arms the proving watch. Runs after storage settles
// (the store lives on machineState) and after the boot's slot is
// known.
func settleSystemRelease(stateRoot, bootSlot string, durable bool, boot *machine.BootStatus) bool {
	if !durable {
		return false
	}
	store := machine.SystemReleases(stateRoot)

	// Republish the standing rejection: boot facts are rebuilt from
	// scratch every boot, but a rejection must outlast the boot that
	// recorded it.
	boot.SystemRejection, _ = store.LoadRejection()

	staged, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the staged release record is unreadable: %v\n", err)
	}
	if staged == nil {
		repairBootOrder(efiVarsDir, stateRoot)
		return false
	}

	record, perr := machine.ParseSystemRelease(staged)
	if perr != nil {
		boot.SystemRejection = rejectStagedSystem(store,
			fmt.Sprintf("the staged release record does not parse: %v", perr), staged)
		repairBootOrder(efiVarsDir, stateRoot)
		return false
	}

	// A boot that didn't come from a slot can't judge a slot trial:
	// external media is running, not either half of blue-green.
	if bootSlot == "" {
		fmt.Printf("liken: system: release %s is staged for slot %s, but this boot came from external media; leaving it for a from-disk boot\n",
			record.Version, record.Slot)
		return false
	}

	if record.Slot == bootSlot {
		// This is the trial: the previous boot armed BootNext at this
		// slot, and this boot is running the staged release's own
		// kernel and init. There is nothing to write, because the
		// attempted marker already stands. The proof isn't init's to
		// declare either: the operator's first reconcile is what shows
		// the machine actually serving its cluster on the new release.
		fmt.Printf("liken: system: proving boot: release %s on slot %s; the operator's first pass is the proof\n",
			record.Version, record.Slot)
		provingTrial = *record
		return true
	}

	hash := machine.ManifestHash(staged)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		// The trial ran and the firmware brought the machine back
		// here, to the proven slot: the release booted its one
		// BootNext chance and never got promoted. That is the
		// fallback verdict, recorded durably so no boot re-arms the
		// same trial.
		boot.SystemRejection = rejectStagedSystem(store,
			fmt.Sprintf("the machine booted slot %s to prove release %s and fell back to slot %s; the release never proved out",
				record.Slot, record.Version, bootSlot), staged)
		repairBootOrder(efiVarsDir, stateRoot)
		return false
	}

	fmt.Printf("liken: system: release %s is staged for slot %s, awaiting its proving reboot\n",
		record.Version, record.Slot)
	repairBootOrder(efiVarsDir, stateRoot)
	return false
}

// rejectStagedSystem quarantines a staged release record with its
// reason, and reports the rejection for this boot's facts.
func rejectStagedSystem(store machine.ManifestStore, reason string, raw []byte) *machine.Rejection {
	fmt.Fprintf(os.Stderr, "liken: system: rejecting the staged release: %s\n", reason)
	rejection := machine.NewRejection(raw, reason, time.Now().UTC())
	if err := store.Reject(rejection); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: recording the rejection: %v\n", err)
	}
	return &rejection
}

// armProvingBoot runs on the reboot path: if a release is staged for
// the other slot, mark it attempted and arm BootNext so the next
// boot, and only the next, tries it. The attempted marker is written
// first, deliberately. A crash between the two writes then reads as
// "tried and fell back", which wrongly rejects a release that never
// ran; but the opposite order could arm a trial with no marker, and
// a failing release would then re-arm and reboot forever. A false
// rejection can be fixed by editing the store; a reboot loop can't
// be fixed by anything, so the ordering favors the false rejection.
func armProvingBoot(efiDir, stateRoot, runningSlot string) {
	store := machine.SystemReleases(stateRoot)
	staged, err := store.LoadStaged()
	if staged == nil || err != nil {
		return
	}
	record, err := machine.ParseSystemRelease(staged)
	if err != nil {
		return // boot-time vetting owns this verdict
	}
	if record.Slot == runningSlot || record.Slot == "" {
		return
	}

	entry, ok := findSlotEntry(efiDir, record.Slot)
	if !ok {
		fmt.Fprintf(os.Stderr, "liken: system: no boot entry answers to \"liken slot %s\"; rebooting without arming the trial\n", record.Slot)
		return
	}

	// The fallback is asserted, and *verified*, before the trial is
	// armed. A trial is only safe when every reset after its one
	// BootNext chance lands on a proven slot, and BootOrder is what
	// guarantees that. If the firmware won't hold the order (or the
	// repair quietly failed), the fallback doesn't exist, and arming
	// a trial anyway could brick the machine with its own upgrade.
	// So the trial is refused, visibly, and the machine stays on the
	// version that works, where an operator can still reach it.
	if !fallbackInPlace(efiDir, stateRoot) {
		fmt.Fprintln(os.Stderr, "liken: system: the fallback BootOrder could not be verified; refusing to arm the trial")
		return
	}

	if err := store.WriteAttempted(machine.ManifestHash(staged)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: marking the trial attempted: %v; rebooting without arming it\n", err)
		return
	}
	if err := writeEFIVar(efiDir, "BootNext", []byte{byte(entry), byte(entry >> 8)}); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: arming BootNext: %v\n", err)
		return
	}
	fmt.Printf("liken: system: BootNext armed at %s; the next boot tries release %s on slot %s, once\n",
		bootEntryID(entry), record.Version, record.Slot)
}

// fallbackInPlace makes BootOrder lead with the proven slot, then
// trusts only what it can read back: a write that appeared to
// succeed is not the same thing as the firmware actually holding the
// order.
func fallbackInPlace(efiDir, stateRoot string) bool {
	repairBootOrder(efiDir, stateRoot)

	proven, err := machine.SystemReleases(stateRoot).LoadProven()
	if proven == nil || err != nil {
		return false
	}
	record, err := machine.ParseSystemRelease(proven)
	if err != nil {
		return false
	}
	leader, ok := findSlotEntry(efiDir, record.Slot)
	if !ok {
		return false
	}
	current := readBootOrder(efiDir)
	return len(current) > 0 && current[0] == leader
}

// provingPatience is how long a proving boot may run unpromoted
// before the watchdog gives up on it: the same ten minutes the
// rollout conductor waits before calling a reboot stalled. A machine
// that hasn't joined its cluster after ten minutes is stuck, not
// merely slow.
const provingPatience = 10 * time.Minute

// provingTrial is the record this boot is trying, set at boot when
// settleSystemRelease recognizes the trial. The proving watch
// compares the store's *proven* record against it, because the trial
// is only over when this exact record has been promoted. A staged
// file that merely disappears was withdrawn, not promoted, and
// treating its absence as promotion would flip BootOrder for a
// promotion that never happened.
var provingTrial machine.SystemRelease

// provingWatch runs only on proving boots, and it combines two
// fallbacks in one loop. The first is the ordinary path: poll the
// store until the operator promotes the staged record (its
// disappearance is the signal), then rewrite BootOrder so the newly
// proven slot leads. The second is a watchdog: a trial that reaches
// provingPatience unpromoted gets a deliberate reboot. The firmware's
// one-shot BootNext is already consumed, so the machine lands on the
// proven slot, where the attempted marker renders the verdict
// (RejectedLastBoot) and prevents any reboot loop. The watchdog
// covers the failure BootNext can't see: a kernel that boots fine
// into a system that never serves.
func provingWatch(ctx context.Context) error {
	deadline := time.After(provingPatience)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			fmt.Fprintf(os.Stderr, "liken: system: the proving boot never settled within %s; rebooting onto the proven slot\n", provingPatience)
			rebootMachine(machine.RebootIntent{
				Reason: "the proving boot never settled; the firmware's consumed BootNext falls back to the proven slot",
			}) // never returns
		case <-time.After(5 * time.Second):
		}
		store := machine.SystemReleases(machine.MachineStateDir)
		staged, err := store.LoadStaged()
		if err != nil || staged != nil {
			continue
		}
		// The staged file is gone, but gone is not the same as
		// promoted. Only a proven record matching this boot's own
		// trial counts as the verdict. Anything else means the record
		// was withdrawn out from under the trial (a retargeted
		// cluster, most likely), and the firmware's order must not
		// change for a promotion that never happened.
		proven, err := store.LoadProven()
		if err != nil || proven == nil {
			continue
		}
		record, err := machine.ParseSystemRelease(proven)
		if err != nil || record.Version != provingTrial.Version || record.Slot != provingTrial.Slot {
			fmt.Printf("liken: system: the trial of release %s was withdrawn without promotion; leaving BootOrder alone\n",
				provingTrial.Version)
			return nil
		}
		fmt.Printf("liken: system: release %s was promoted; asserting BootOrder from the store\n", record.Version)
		repairBootOrder(efiVarsDir, machine.MachineStateDir)
		return nil
	}
}

// repairBootOrder makes the firmware agree with the store: the proven
// record's slot leads BootOrder, and everything else keeps its
// relative order. It runs on every boot and after every promotion,
// so the store stays the authority and the firmware only ever holds
// a copy. A lost or mangled BootOrder (a dead NVRAM battery, someone
// editing the setup menu) is corrected on the next boot.
func repairBootOrder(efiDir, stateRoot string) {
	proven, err := machine.SystemReleases(stateRoot).LoadProven()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the proven release record is unreadable: %v\n", err)
		return
	}
	if proven == nil {
		return // no record yet; leave the firmware's order as it is
	}
	record, err := machine.ParseSystemRelease(proven)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the proven release record does not parse: %v\n", err)
		return
	}
	leader, ok := findSlotEntry(efiDir, record.Slot)
	if !ok {
		fmt.Fprintf(os.Stderr, "liken: system: the store says slot %s is proven, but no boot entry answers to it; leaving BootOrder alone\n", record.Slot)
		return
	}

	order := readBootOrder(efiDir)
	if len(order) > 0 && order[0] == leader {
		return // the firmware already agrees
	}

	repaired := []uint16{leader}
	for _, n := range order {
		if n != leader {
			repaired = append(repaired, n)
		}
	}
	if err := writeBootOrder(efiDir, repaired); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: repairing BootOrder: %v\n", err)
		return
	}
	// Trust the readback, not the write: some firmware accepts a
	// write and then fails to hold it, and every later report would
	// be wrong if the write were taken at face value.
	if readback := readBootOrder(efiDir); len(readback) == 0 || readback[0] != leader {
		fmt.Fprintf(os.Stderr, "liken: system: BootOrder was written but reads back unchanged; the firmware is not holding it\n")
		return
	}
	fmt.Printf("liken: system: BootOrder now leads with %s (slot %s is proven)\n",
		bootEntryID(leader), record.Slot)
}

// findSlotEntry locates a slot's firmware entry the way everything in
// liken finds things: by the name written on it at install time.
func findSlotEntry(efiDir, slot string) (uint16, bool) {
	for number, option := range listBootEntries(efiDir) {
		if option.description == "liken slot "+slot {
			return number, true
		}
	}
	return 0, false
}
