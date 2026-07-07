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
//     reset after that — a kernel panic, a watchdog, a power cut —
//     falls back to BootOrder, which still prefers the proven slot.
//
//   - On the way up (settleSystemRelease), init reads the story the
//     files tell. Booted the staged slot: this is the trial, and the
//     operator's first reconcile — living proof that the new kernel,
//     init, k3s, and the join all work — will promote it. Booted the
//     proven slot with the attempted marker set: the trial ran and
//     the firmware brought us back, so the record is rejected
//     durably, and the operator refuses to stage that exact decision
//     again.
//
//   - After promotion (provingWatch, a machine-plane component that
//     runs only on proving boots), init flips BootOrder so the slot
//     that just proved itself leads. Init owns this write for the
//     same reason it owns every efivars write: the firmware
//     conversation is the machine plane's, and the store is its
//     authority — repairBootOrder re-asserts the proven record's slot
//     on every boot, so a power cut between promotion and flip costs
//     one extra boot of the old version, never a wrong BootOrder that
//     sticks.

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

	// The standing rejection, republished every boot: facts die with
	// the boot, this record must not.
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
		// slot and here we are, running the staged release's own
		// kernel and init. Nothing to write — the attempted marker
		// already stands — and the proof isn't init's to declare: the
		// operator's first reconcile is the machine demonstrably
		// serving its cluster on the new release.
		fmt.Printf("liken: system: proving boot: release %s on slot %s; the operator's first pass is the proof\n",
			record.Version, record.Slot)
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
	rejection := machine.Rejection{
		Hash:       machine.ManifestHash(raw),
		Reason:     reason,
		RejectedAt: time.Now().UTC(),
	}
	if err := store.Reject(rejection); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: recording the rejection: %v\n", err)
	}
	return &rejection
}

// armProvingBoot is the reboot path's stop at the firmware: if a
// release is staged for the other slot, mark it attempted and arm
// BootNext so the next boot — and only the next — tries it. The
// attempted marker lands first, deliberately: a crash between the two
// writes then reads as "tried and fell back", which wrongly rejects a
// release that never ran — but the opposite order could arm a trial
// with no marker, and a failing release would re-arm and reboot
// forever. A false rejection is recoverable by an edit; a reboot loop
// is not recoverable by anything. Down is recoverable, wrong is not.
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

// provingPatience is how long a proving boot may run unpromoted
// before the watchdog gives up on it: the same ten minutes the
// rollout conductor waits before calling a reboot stalled. A machine
// that can't join its cluster in ten minutes isn't warming up, it's
// wedged.
const provingPatience = 10 * time.Minute

// provingWatch runs only on proving boots, and it is two fallbacks in
// one loop. The happy half: poll the store until the operator
// promotes the staged record (its disappearance is the signal), then
// flip BootOrder so the slot that just proved itself leads. The
// watchdog half: a trial that reaches provingPatience unpromoted gets
// a deliberate reboot — the firmware's one-shot BootNext is already
// consumed, so the machine lands on the proven slot, where the
// attempted marker renders the verdict (RejectedLastBoot) and breaks
// any possibility of a loop. This is the fallback for the failure
// BootNext can't see: a kernel that boots fine into a system that
// never serves.
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
		staged, err := machine.SystemReleases(machine.MachineStateDir).LoadStaged()
		if err != nil || staged != nil {
			continue
		}
		fmt.Println("liken: system: the staged release was promoted; asserting BootOrder from the store")
		repairBootOrder(efiVarsDir, machine.MachineStateDir)
		return nil
	}
}

// repairBootOrder makes the firmware agree with the store: the proven
// record's slot leads BootOrder, everything else keeps its relative
// order. Run on every boot and after every promotion, so the
// firmware's memory is only ever a cache of the machine's own — a
// lost or mangled BootOrder (a dead NVRAM battery, a curious hand in
// the setup menu) heals on the next boot.
func repairBootOrder(efiDir, stateRoot string) {
	proven, err := machine.SystemReleases(stateRoot).LoadProven()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the proven release record is unreadable: %v\n", err)
		return
	}
	if proven == nil {
		return // no record yet; the firmware's order is the only opinion
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

	current, _ := readEFIVar(efiDir, "BootOrder")
	var order []uint16
	for i := 0; i+2 <= len(current); i += 2 {
		order = append(order, uint16(current[i])|uint16(current[i+1])<<8)
	}
	if len(order) > 0 && order[0] == leader {
		return // the cache already agrees
	}

	repaired := []uint16{leader}
	for _, n := range order {
		if n != leader {
			repaired = append(repaired, n)
		}
	}
	payload := make([]byte, len(repaired)*2)
	for i, n := range repaired {
		payload[i*2] = byte(n)
		payload[i*2+1] = byte(n >> 8)
	}
	if err := writeEFIVar(efiDir, "BootOrder", payload); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: repairing BootOrder: %v\n", err)
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
