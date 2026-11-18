package main

// A downloaded release becomes the running release after a trial
// boot.
//
// When a verified release is on the inactive slot, the operator writes
// a staged SystemRelease record (machine/systemrelease.go). The code
// here boots that slot once as a trial, and falls back to the proven
// slot when the trial fails. Every firmware write goes through a
// bootActuator (actuator.go), which has a version for UEFI and one for
// GRUB. The default boot order is BootOrder on UEFI and default_slot
// on GRUB. The one-shot boot is BootNext on UEFI and try_slot on GRUB.
//
//   - On the reboot path, armProvingBoot writes the attempted marker
//     and arms a one-shot boot of the staged slot. The firmware clears
//     the one-shot when it boots it, so the staged release gets one
//     try. A later reset, such as a kernel panic, a watchdog reset, or
//     a power cut, follows the default boot order, which still puts
//     the proven slot first.
//
//   - At boot, settleSystemRelease reads the store. A boot from the
//     staged slot is the trial. The operator promotes the release when
//     its first reconcile on that boot succeeds, which shows that the
//     new kernel, init, and k3s work and the machine joined its
//     cluster. A boot from the other slot, with an attempted marker
//     that matches the staged record, means the trial ran and the
//     machine fell back. Init then writes a rejection to the store, and
//     the operator does not stage that record again.
//
//   - On a trial boot, provingWatch waits for the promotion and then
//     puts the new proven slot first in the default boot order. The
//     operator writes only the store, and init copies the store's
//     proven slot into the firmware. If power is cut after the
//     promotion and before that write, the next boot runs the old
//     release once more, and settleSystemRelease corrects the
//     preference.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// settleSystemRelease decides whether this boot is the trial of a
// staged release, the fallback from one, or an ordinary boot. On a
// trial it returns the staged record, and main starts the proving
// watch with it. Otherwise it returns nil. When machineState is not on
// a durable partition, it does nothing. The store is on machineState,
// so main calls this after storage is mounted.
func settleSystemRelease(act bootActuator, stateRoot, bootSlot string, durable bool, boot *machine.BootStatus) *machine.SystemRelease {
	if !durable {
		return nil
	}
	store := machine.SystemReleases(stateRoot)

	// BootStatus starts empty on every boot, so the stored rejection is
	// copied into it each time.
	boot.SystemRejection, _ = store.LoadRejection()

	staged, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: reading the staged release record: %v\n", err)
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

	// A boot from external media ran neither slot, so it cannot tell
	// whether a trial ran.
	if bootSlot == "" {
		fmt.Printf("liken: system: release %s is staged for slot %s, but this boot came from external media, so init leaves it for a boot from the disk\n",
			record.Version, record.Slot)
		return nil
	}

	if record.Slot == bootSlot {
		// This is the trial. The reboot path already wrote the attempted
		// marker, and the operator, not init, decides whether the trial
		// succeeded.
		fmt.Printf("liken: system: this is the trial boot of release %s on slot %s, and the operator decides whether it succeeded\n",
			record.Version, record.Slot)
		return record
	}

	hash := machine.ManifestHash(staged)
	if attempted, _ := store.LoadAttempted(); attempted == hash {
		// The trial ran and the machine came back on the proven slot
		// without a promotion. The rejection is written to the store so
		// that no later reboot arms the same trial.
		boot.SystemRejection = rejectStagedDocument("system", "release", store.Reject,
			staged, fmt.Sprintf("the machine booted slot %s to try release %s, then booted slot %s before the operator promoted the release",
				record.Slot, record.Version, bootSlot))
		assertProvenSlot(act, stateRoot)
		return nil
	}

	fmt.Printf("liken: system: release %s is staged for slot %s and waits for a reboot to try it\n",
		record.Version, record.Slot)
	assertProvenSlot(act, stateRoot)
	return nil
}

// armProvingBoot runs on the reboot path. When a release is staged for
// the other slot, it writes the attempted marker and then arms the
// one-shot boot of that slot.
//
// The marker is written first. If the machine crashes between the two
// writes, the next boot finds a marker with no trial and rejects a
// release that never ran, and a person can clear that rejection by
// editing the store. In the other order, a crash leaves a trial with no
// marker. A release that fails its trial would then never be rejected,
// and every reboot would arm it again.
//
// Every check that can refuse the trial, including canArmTrial, runs
// before either write, so a refusal leaves no false rejection.
func armProvingBoot(act bootActuator, stateRoot, runningSlot string) {
	store := machine.SystemReleases(stateRoot)
	staged, err := store.LoadStaged()
	if staged == nil || err != nil {
		return
	}
	record, err := machine.ParseSystemRelease(staged)
	if err != nil {
		return // settleSystemRelease rejects an unparseable record at boot
	}
	if record.Slot == runningSlot || record.Slot == "" {
		return
	}

	// The running slot is not always the proven slot. A person can pick
	// the other slot in the firmware menu, and a firmware that cannot
	// load the proven slot's entry boots the other one. A release staged
	// on the proven slot must not be tried, because a failed trial
	// falls back to that same slot.
	if proven := provenRecord(stateRoot); proven != nil && record.Slot == proven.Slot {
		fmt.Fprintf(os.Stderr, "liken: system: release %s is staged for slot %s, the proven slot, so the reboot does not arm its trial\n",
			record.Version, record.Slot)
		return
	}

	if err := checkStagedSlot(slotMountPath(record.Slot), record); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: slot %s does not hold release %s as staged: %v, so the reboot does not arm its trial\n",
			record.Slot, record.Version, err)
		return
	}

	if err := act.canArmTrial(record.Slot); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v, so the reboot does not arm the trial\n", err)
		return
	}

	// A trial is safe only when every reset after it boots the proven
	// slot. When the default boot order does not read back with the
	// proven slot first, a reset after a failed trial could boot the
	// failed release again, so the machine stays on the release that
	// works.
	if !fallbackInPlace(act, stateRoot) {
		fmt.Fprintln(os.Stderr, "liken: system: the default boot order does not read back with the proven slot first, so the reboot does not arm the trial")
		return
	}

	if err := store.WriteAttempted(machine.ManifestHash(staged)); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: writing the attempted marker of the trial: %v, so the reboot does not arm it\n", err)
		return
	}
	armed, err := act.armTrial(record.Slot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: arming the trial of release %s on slot %s: %v\n", record.Version, record.Slot, err)
		return
	}
	fmt.Printf("liken: system: %s, so the next boot tries release %s on slot %s once\n",
		armed, record.Version, record.Slot)
}

// checkStagedSlot returns an error unless the slot still has exactly
// the release that the staged record names: release.yaml must have the
// record's digest, and every artifact must match release.yaml. The
// slot can change after the operator writes the record, when a new
// target starts a download of another release onto the same slot and
// that download stops partway. A trial of that slot would boot files
// of two releases.
func checkStagedSlot(mount string, record *machine.SystemRelease) error {
	if mount == "" {
		return errors.New("the slot is not mounted")
	}
	raw, err := os.ReadFile(filepath.Join(mount, "release.yaml"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if digest := "sha256:" + hex.EncodeToString(sum[:]); digest != record.ReleaseDigest {
		return fmt.Errorf("release.yaml of the slot has digest %s, and the staged record names %s", digest, record.ReleaseDigest)
	}
	return verifySlotContents(mount)
}

// fallbackInPlace makes the proven slot first in the default boot
// order and then reads it back through the actuator. A write
// that returns without error does not prove that the firmware kept it.
func fallbackInPlace(act bootActuator, stateRoot string) bool {
	record := provenRecord(stateRoot)
	if record == nil {
		return false
	}
	act.assertProven(record.Slot)
	return act.fallbackLeads(record.Slot)
}

// provingTimeout is how long a trial boot may run without promotion
// before the proving watch reboots the machine. It equals
// rolloutStallAfter in cluster-operator, the time after which the
// Cluster's rollout marks a reboot as stalled.
const provingTimeout = 10 * time.Minute

// provingWatch returns the machine-plane component that runs on a
// trial boot. Every five seconds it reads the store. When the staged
// record is gone and the proven record is this trial's release, the
// operator has promoted it, and the watch puts the proven slot first
// in the default boot order. When the staged record is gone and the
// proven record names another release, the operator withdrew the
// staged record, for example because the cluster's target moved to
// another release. The watch then stops and leaves BootOrder alone.
// While there is no proven record, it keeps waiting.
//
// If provingTimeout passes without promotion, the watch reboots the
// machine. The firmware has already cleared the one-shot, so the
// machine boots the proven slot, and the attempted marker makes that
// boot reject the release. This covers a release that boots but never
// joins its cluster.
func provingWatch(act bootActuator, trial machine.SystemRelease, stateRoot string, tree machine.FactsTree) func(context.Context) error {
	return func(ctx context.Context) error {
		deadline := time.After(provingTimeout)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-deadline:
				fmt.Fprintf(os.Stderr, "liken: system: release %s on slot %s was not promoted within %s, so init reboots onto the proven slot\n", trial.Version, trial.Slot, provingTimeout)
				rebootMachine(machine.RebootIntent{
					Reason: fmt.Sprintf("release %s on slot %s was not promoted within %s", trial.Version, trial.Slot, provingTimeout),
				}) // never returns
			case <-time.After(5 * time.Second):
			}
			store := machine.SystemReleases(stateRoot)
			staged, err := store.LoadStaged()
			if err != nil || staged != nil {
				continue
			}
			proven, err := store.LoadProven()
			if err != nil || proven == nil {
				continue
			}
			record, err := machine.ParseSystemRelease(proven)
			if err != nil || record.Version != trial.Version || record.Slot != trial.Slot {
				fmt.Printf("liken: system: the operator withdrew the trial of release %s without promoting it, so init leaves BootOrder as it is\n",
					trial.Version)
				return nil
			}
			fmt.Printf("liken: system: the operator promoted release %s, so init sets the proven slot first from the store\n", record.Version)
			assertProvenSlotUnderLock(act, stateRoot)
			// The boot published the firmware facts before this write
			// changed BootOrder and BootNext. Publish them again, or
			// status.firmware keeps the old order until the next boot.
			tree.WriteFirmware(firmwareFacts(efiVarsDir))
			return nil
		}
	}
}

// assertProvenSlotUnderLock is assertProvenSlot for the proving watch,
// which can run at the same time as the reboot path. It checks
// shuttingDown after it takes the lock. When the watch waited on the
// lock while the reboot path armed a trial, it then reads the flag
// that the reboot path set, and does not undo the trial.
func assertProvenSlotUnderLock(act bootActuator, stateRoot string) {
	firmwareWrites.Lock()
	defer firmwareWrites.Unlock()
	if shuttingDown.Load() {
		return
	}
	assertProvenSlot(act, stateRoot)
}

// assertAndArmForReboot makes every firmware write of the reboot path
// under one lock, so that no other write comes between asserting the
// proven slot and arming the trial. The unlock is deferred because the
// reboot path can run inside a machine-plane component, and the
// machine plane recovers a component's panic and restarts it. A lock
// still held after a panic would block every later reboot.
//
// handover is the name of the Disruption that set BootNext for this
// reboot, or empty. When init accepts the handover (handover.go), it
// skips both writes and leaves BootNext as the Disruption set it. When
// init refuses it, the ordinary writes run.
func assertAndArmForReboot(act bootActuator, stateRoot, runningSlot, handover string) {
	firmwareWrites.Lock()
	defer firmwareWrites.Unlock()
	if handover != "" {
		err := handOverOneShot(act, stateRoot)
		if err == nil {
			fmt.Printf("liken: system: the Disruption %s set BootNext for this reboot, so init leaves it, and a staged release waits for a later reboot\n", handover)
			return
		}
		fmt.Fprintf(os.Stderr, "liken: system: init does not leave BootNext as the Disruption %s set it: %v\n", handover, err)
		if errors.Is(err, errNoProvenSlot) {
			act.dropOneShot()
		}
	}
	assertProvenSlot(act, stateRoot)
	armProvingBoot(act, stateRoot, runningSlot)
}

// assertProvenSlot puts the proven record's slot first in the default
// boot order. The store is the source of truth, and the firmware
// is a copy that can change under it, for example after a reset of
// the firmware's settings or an edit in the firmware menu. So this
// runs on each boot except a trial or a boot from external media, on
// each reboot, and after each promotion.
func assertProvenSlot(act bootActuator, stateRoot string) {
	record := provenRecord(stateRoot)
	if record == nil {
		return // a machine that never upgraded has no proven record
	}
	act.assertProven(record.Slot)
}

// provenRecord returns the store's proven release record, or nil. A
// record that is unreadable or does not parse prints a console line,
// because the machine then has no fallback. A missing record is normal
// on a machine that has never upgraded.
func provenRecord(stateRoot string) *machine.SystemRelease {
	proven, err := machine.SystemReleases(stateRoot).LoadProven()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: reading the proven release record: %v\n", err)
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
