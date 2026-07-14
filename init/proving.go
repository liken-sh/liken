package main

// The proving boot: how a downloaded release becomes the running one.
//
// The operator stages a SystemRelease record (machine/systemrelease.go)
// when a verified release is waiting on the inactive slot. From
// there, this file carries the upgrade across the reboot. Everything
// here is firmware-neutral: the three acts that touch the machine's
// actual boot mechanism go through a bootActuator (actuator.go),
// which carries the firmware's dialect.
//
//   - On the way down (armProvingBoot, called from init's reboot
//     path), init writes the attempted marker and arms the
//     actuator's one-shot trial at the staged slot. The one-shot is
//     what makes blue-green safe: the arming is consumed by the boot
//     it triggers, so the trial gets exactly one chance, and any
//     reset after that (a kernel panic, a watchdog, a power cut)
//     falls back to the standing preference, which still prefers the
//     proven slot.
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
//     runs only on proving boots), init asserts the standing
//     preference so the newly proven slot leads. Init owns this
//     write for the same reason it owns every firmware write:
//     talking to the firmware is the machine plane's job, and the
//     store on disk is the authority. assertProvenSlot re-asserts
//     the proven record's slot on every boot, so a power cut between
//     promotion and the assertion costs one extra boot of the old
//     version, never a wrong preference that sticks.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/machine"
)

// settleSystemRelease reads the system store at boot and renders the
// verdicts only init can: is this boot a trial, or the fallback from
// one? When this boot is proving a staged release it returns that
// release's record, which is what arms the proving watch; every other
// verdict returns nil. Runs after storage settles (the store lives on
// machineState) and after the boot's slot is known.
func settleSystemRelease(act bootActuator, stateRoot, bootSlot string, durable bool, boot *machine.BootStatus) *machine.SystemRelease {
	if !durable {
		return nil
	}
	store := machine.SystemReleases(stateRoot)

	// The standing rejection is republished into the boot record
	// every boot (rejectStagedDocument explains why).
	boot.SystemRejection, _ = store.LoadRejection()

	staged, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the staged release record is unreadable: %v\n", err)
	}
	if staged == nil {
		assertProvenSlot(act, stateRoot)
		return nil
	}

	record, perr := machine.ParseSystemRelease(staged)
	if perr != nil {
		boot.SystemRejection = rejectStagedDocument("system", "release", store.Reject,
			staged, fmt.Sprintf("the staged release record does not parse: %v", perr))
		assertProvenSlot(act, stateRoot)
		return nil
	}

	// A boot that didn't come from a slot can't judge a slot trial:
	// external media is running, not either half of blue-green.
	if bootSlot == "" {
		fmt.Printf("liken: system: release %s is staged for slot %s, but this boot came from external media; leaving it for a from-disk boot\n",
			record.Version, record.Slot)
		return nil
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
		return record
	}

	hash := machine.ManifestHash(staged)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		// The trial ran and the firmware brought the machine back
		// here, to the proven slot: the release booted its one
		// chance and never got promoted. That is the fallback
		// verdict, recorded durably so no boot re-arms the same
		// trial.
		boot.SystemRejection = rejectStagedDocument("system", "release", store.Reject,
			staged, fmt.Sprintf("the machine booted slot %s to prove release %s and fell back to slot %s; the release never proved out",
				record.Slot, record.Version, bootSlot))
		assertProvenSlot(act, stateRoot)
		return nil
	}

	fmt.Printf("liken: system: release %s is staged for slot %s, awaiting its proving reboot\n",
		record.Version, record.Slot)
	assertProvenSlot(act, stateRoot)
	return nil
}

// armProvingBoot runs on the reboot path: if a release is staged for
// the other slot, mark it attempted and arm the actuator's one-shot
// trial so the next boot, and only the next, tries it. The attempted
// marker is written first, deliberately. A crash between the two
// writes then reads as "tried and fell back", which wrongly rejects
// a release that never ran; but the opposite order could arm a trial
// with no marker, and a failing release would then re-arm and reboot
// forever. A false rejection can be fixed by editing the store; a
// reboot loop can't be fixed by anything, so the ordering favors the
// false rejection. canArmTrial runs before either write for the same
// reason: anything knowably wrong must be found while refusing is
// still free.
func armProvingBoot(act bootActuator, stateRoot, runningSlot string) {
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

	if err := act.canArmTrial(record.Slot); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v; rebooting without arming the trial\n", err)
		return
	}

	// The fallback is asserted, and *verified*, before the trial is
	// armed. A trial is only safe when every reset after its one
	// chance lands on a proven slot, and the standing preference is
	// what guarantees that. If the firmware won't hold it (or the
	// assertion quietly failed), the fallback doesn't exist, and
	// arming a trial anyway could brick the machine with its own
	// upgrade. So the trial is refused, visibly, and the machine
	// stays on the version that works, where an operator can still
	// reach it.
	if !fallbackInPlace(act, stateRoot) {
		fmt.Fprintln(os.Stderr, "liken: system: the proven fallback could not be verified; refusing to arm the trial")
		return
	}

	if err := store.WriteAttempted(machine.ManifestHash(staged)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: marking the trial attempted: %v; rebooting without arming it\n", err)
		return
	}
	armed, err := act.armTrial(record.Slot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
		return
	}
	fmt.Printf("liken: system: %s; the next boot tries release %s on slot %s, once\n",
		armed, record.Version, record.Slot)
}

// fallbackInPlace asserts the standing preference at the proven slot,
// then trusts only what the actuator can read back: a write that
// appeared to succeed is not the same thing as the firmware actually
// holding it.
func fallbackInPlace(act bootActuator, stateRoot string) bool {
	record := provenRecord(stateRoot)
	if record == nil {
		return false
	}
	act.assertProven(record.Slot)
	return act.fallbackLeads(record.Slot)
}

// provingPatience is how long a proving boot may run unpromoted
// before the watchdog gives up on it: the same ten minutes the
// rollout conductor waits before calling a reboot stalled. A machine
// that hasn't joined its cluster after ten minutes is stuck, not
// merely slow.
const provingPatience = 10 * time.Minute

// provingWatch builds the machine-plane component a proving boot
// runs, closed over the record this boot is trying. The watch
// compares the store's *proven* record against the trial's own,
// because the trial is only over when this exact record has been
// promoted: a staged file that merely disappears was withdrawn, not
// promoted, and treating its absence as promotion would flip
// BootOrder for a promotion that never happened.
//
// The loop combines two fallbacks. The first is the ordinary path:
// poll the store until the operator promotes the staged record (its
// disappearance is the signal), then assert the standing preference
// so the newly proven slot leads. The second is a watchdog: a trial
// that reaches provingPatience unpromoted gets a deliberate reboot.
// The one-shot arming is already consumed, so the machine lands on
// the proven slot, where the attempted marker renders the verdict
// (RejectedLastBoot) and prevents any reboot loop. The watchdog
// covers the failure the one-shot can't see: a kernel that boots
// fine into a system that never serves.
func provingWatch(act bootActuator, trial machine.SystemRelease) func(context.Context) error {
	return func(ctx context.Context) error {
		deadline := time.After(provingPatience)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-deadline:
				fmt.Fprintf(os.Stderr, "liken: system: the proving boot never settled within %s; rebooting onto the proven slot\n", provingPatience)
				rebootMachine(machine.RebootIntent{
					Reason: "the proving boot never settled; the consumed one-shot falls back to the proven slot",
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
			// trial counts as the verdict. Anything else means the
			// record was withdrawn out from under the trial (a
			// retargeted cluster, most likely), and the firmware's
			// order must not change for a promotion that never
			// happened.
			proven, err := store.LoadProven()
			if err != nil || proven == nil {
				continue
			}
			record, err := machine.ParseSystemRelease(proven)
			if err != nil || record.Version != trial.Version || record.Slot != trial.Slot {
				fmt.Printf("liken: system: the trial of release %s was withdrawn without promotion; leaving BootOrder alone\n",
					trial.Version)
				return nil
			}
			fmt.Printf("liken: system: release %s was promoted; asserting the proven slot from the store\n", record.Version)
			assertProvenSlot(act, machine.MachineStateDir)
			return nil
		}
	}
}

// assertProvenSlot makes the firmware agree with the store: the
// proven record's slot leads the standing boot preference. It runs on
// every boot and after every promotion, so the store stays the
// authority and the firmware only ever holds a copy; whatever
// drifted (a dead NVRAM battery, someone editing the setup menu) is
// corrected on the next boot. Reading the record is neutral work done
// here; making the firmware agree is the actuator's.
func assertProvenSlot(act bootActuator, stateRoot string) {
	record := provenRecord(stateRoot)
	if record == nil {
		return // no record yet; leave the firmware's preference as it is
	}
	act.assertProven(record.Slot)
}

// provenRecord loads and parses the store's proven release record,
// reporting the ways a record can fail to exist: unreadable and
// unparseable earn a console line, because a machine whose proven
// record is damaged has lost its fallback; absent is ordinary (a
// machine that has never upgraded).
func provenRecord(stateRoot string) *machine.SystemRelease {
	proven, err := machine.SystemReleases(stateRoot).LoadProven()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the proven release record is unreadable: %v\n", err)
		return nil
	}
	if proven == nil {
		return nil
	}
	record, err := machine.ParseSystemRelease(proven)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: the proven release record does not parse: %v\n", err)
		return nil
	}
	return record
}
