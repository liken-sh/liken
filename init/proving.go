package main

// The proving boot: how a downloaded release becomes the running
// release.
//
// The operator stages a SystemRelease record (machine/systemrelease.go)
// when a verified release is waiting on the inactive slot. From that
// point, this file carries the upgrade across the reboot. Everything
// in this file is firmware-neutral. The three actions that touch the
// machine's actual boot mechanism go through a bootActuator
// (actuator.go), which carries the firmware's own dialect.
//
//   - Before the reboot (armProvingBoot, called from init's reboot
//     path), init writes the attempted marker and arms the
//     actuator's one-shot trial at the staged slot. The one-shot
//     mechanism is what makes blue-green upgrades safe. The boot
//     that the arming triggers consumes the arming, so the trial
//     gets exactly one chance. Any reset after that chance, such as
//     a kernel panic, a watchdog reset, or a power cut, falls back
//     to the standing preference, which still prefers the proven
//     slot.
//
//   - After the reboot (settleSystemRelease), init reads the files
//     and decides what happened. If this boot came from the staged
//     slot, this boot is the trial. The operator's first reconcile
//     shows that the new kernel, init, k3s, and the cluster join all
//     work, and this is what promotes the release. If this boot came
//     from the proven slot with the attempted marker set, the trial
//     ran and the firmware fell back. In this case the record is
//     rejected durably, and the operator will not stage that exact
//     record again.
//
//   - After promotion (provingWatch, a machine-plane component that
//     runs only on proving boots), init asserts the standing
//     preference so the newly proven slot leads. Init owns this
//     write for the same reason it owns every firmware write:
//     talking to the firmware is the machine plane's job, and the
//     store on disk is the authority. assertProvenSlot re-asserts
//     the proven record's slot on every boot. So, if a power cut
//     happens between promotion and the assertion, the machine costs
//     one extra boot of the old version, and never keeps a wrong
//     preference.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/machine"
)

// settleSystemRelease reads the system store at boot and decides the
// verdict that only init can decide: is this boot a trial, or is it
// the fallback from a trial? When this boot proves a staged release,
// the function returns that release's record. This record is what
// arms the proving watch. Every other verdict returns nil. This
// function runs after storage settles (the store lives on
// machineState) and after the code knows the boot's slot.
func settleSystemRelease(act bootActuator, stateRoot, bootSlot string, durable bool, boot *machine.BootStatus) *machine.SystemRelease {
	if !durable {
		return nil
	}
	store := machine.SystemReleases(stateRoot)

	// The code republishes the standing rejection into the boot
	// record on every boot. rejectStagedDocument explains why.
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

	// A boot that did not come from a slot cannot judge a slot
	// trial. In this case, external media is running, not either
	// half of the blue-green pair.
	if bootSlot == "" {
		fmt.Printf("liken: system: release %s is staged for slot %s, but this boot came from external media; leaving it for a from-disk boot\n",
			record.Version, record.Slot)
		return nil
	}

	if record.Slot == bootSlot {
		// This boot is the trial. The previous boot armed BootNext at
		// this slot, and this boot runs the staged release's own
		// kernel and init. The code writes nothing here, because the
		// attempted marker already stands. init also does not decide
		// the proof. The operator's first reconcile is what shows
		// that the machine actually serves its cluster on the new
		// release.
		fmt.Printf("liken: system: proving boot: release %s on slot %s; the operator's first pass is the proof\n",
			record.Version, record.Slot)
		return record
	}

	hash := machine.ManifestHash(staged)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		// The trial ran, and the firmware brought the machine back
		// to the proven slot. The release used its one chance and
		// did not get promoted. This is the fallback verdict. The
		// code records it durably so that no later boot re-arms the
		// same trial.
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

// armProvingBoot runs on the reboot path. If a release is staged for
// the other slot, the function marks it attempted and arms the
// actuator's one-shot trial. Only the next boot tries the staged
// release.
//
// The code writes the attempted marker first, deliberately. If a
// crash happens between the two writes, the boot record reads as
// "tried and fell back". This wrongly rejects a release that never
// ran. But the opposite write order could arm a trial with no
// marker. A failing release would then re-arm itself and reboot
// forever. An operator can fix a false rejection by editing the
// store, but nothing can fix a reboot loop. So the write order
// favors the false rejection over the reboot loop.
//
// canArmTrial runs before either write, for the same reason: the
// code must find anything that is knowably wrong while refusing the
// trial still costs nothing.
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

	// The code asserts the fallback, and verifies it, before it arms
	// the trial. A trial is only safe when every reset after its one
	// chance lands on a proven slot. The standing preference is what
	// guarantees this. If the firmware does not hold the preference,
	// or if the assertion quietly fails, the fallback does not
	// exist. Arming a trial in that case could make the machine
	// permanently unusable, through its own upgrade. So the code
	// refuses the trial, visibly, and the machine stays on the
	// version that works, where an operator can still reach it.
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

// fallbackInPlace asserts the standing preference at the proven
// slot. Then it checks only what the actuator can read back. A
// write that appears to succeed is not the same as proof that the
// firmware actually holds it.
func fallbackInPlace(act bootActuator, stateRoot string) bool {
	record := provenRecord(stateRoot)
	if record == nil {
		return false
	}
	act.assertProven(record.Slot)
	return act.fallbackLeads(record.Slot)
}

// provingPatience is the length of time a proving boot may run
// unpromoted before the watchdog reboots it. This is the same ten
// minutes that the rollout conductor waits before it calls a reboot
// stalled. A machine that has not joined its cluster after ten
// minutes is stuck, not merely slow.
const provingPatience = 10 * time.Minute

// provingWatch builds the machine-plane component that a proving
// boot runs. This component closes over the record that this boot
// is trying. The watch compares the store's proven record against
// the trial's own record. The trial is only over when the code has
// promoted this exact record. A staged file that merely disappears
// was withdrawn, not promoted. Treating its absence as promotion
// would flip BootOrder for a promotion that never happened.
//
// The loop combines two fallback paths. The first is the ordinary
// path: the code polls the store until the operator promotes the
// staged record (the record's disappearance is the signal), then it
// asserts the standing preference so the newly proven slot leads.
// The second path is a watchdog: when a trial reaches
// provingPatience unpromoted, the code forces a deliberate reboot.
// The one-shot arming is already consumed, so the machine lands on
// the proven slot. There, the attempted marker sets the verdict
// (RejectedLastBoot) and prevents any reboot loop. The watchdog
// covers a failure that the one-shot cannot detect: a kernel that
// boots fine into a system that never serves.
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
			// promoted. Only a proven record that matches this
			// boot's own trial counts as the verdict. Anything else
			// means that someone withdrew the record while the
			// trial was still running, most likely because of a
			// retargeted cluster. In that case, the firmware's boot
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

// assertProvenSlot sets the firmware to match the store: the proven
// record's slot leads the standing boot preference. This function
// runs on every boot and after every promotion. So the store stays
// the authority, and the firmware only ever holds a copy of it. The
// code corrects anything that drifted, such as a dead NVRAM battery
// or someone editing the setup menu, on the next boot. Reading the
// record is plain work that this function does directly. Making the
// firmware match the record is the actuator's job.
func assertProvenSlot(act bootActuator, stateRoot string) {
	record := provenRecord(stateRoot)
	if record == nil {
		return // no record yet; leave the firmware's preference as it is
	}
	act.assertProven(record.Slot)
}

// provenRecord loads and parses the store's proven release record.
// It reports the ways that a record can fail to exist. An unreadable
// or unparseable record prints a console line, because a machine
// with a damaged proven record has lost its fallback. An absent
// record is ordinary; it means the machine has never upgraded.
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
