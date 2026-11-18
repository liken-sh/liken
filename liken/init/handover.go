package main

// On some reboots, init leaves BootNext as the program that requested
// the reboot set it.
//
// On an ordinary reboot, init sets BootNext to the proven slot, or to
// the slot of a staged release for its trial (proving.go). A UEFI
// capsule update needs BootNext to stay as the requester set it: fwupd
// sets BootNext to its updater, the firmware boots the updater once,
// the updater passes the capsule to the firmware, and the firmware
// applies it and resets. This file calls that a handover.
//
// A requester such as firmware-operator sets spec.keepBootNext on its
// Disruption after it stages a capsule, and machine-operator writes the
// Disruption's name in the reboot intent. For that reboot, init leaves
// BootNext alone and arms no trial, so a staged release waits for a
// later reboot. init still repairs the slots' boot entries and
// BootOrder, and writes the fallback loader to the proven slot.
//
// The firmware consumes BootNext when it boots the updater, so the boot
// after that follows BootOrder. init accepts the handover only when
// that boot is certain to reach the proven slot:
//
//   - the release store records a proven slot,
//   - the proven slot led BootOrder at power-on, so the firmware kept
//     the order through the last reset,
//   - BootNext does not point at the other slot's entry, and
//   - the proven slot has the fallback loader at the firmware's default
//     boot path, for a firmware that has lost its boot entries.
//
// A firmware update cannot be undone, so these checks are stricter than
// the ones for a trial. When a check fails, init prints the reason and
// does an ordinary reboot, and the requester's next scan finds that its
// update did not run.

import (
	"errors"
	"fmt"
	"os"
)

// bootOrderKept records, from before init wrote anything at power-on,
// whether the proven slot led BootOrder. It is nil when init could not
// tell, for example when the store records no proven slot. main sets
// it once, from observeBootOrder.
var bootOrderKept *bool

// observeBootOrder reports whether the proven slot's entry leads
// BootOrder. Every reboot puts the proven slot first in BootOrder, so
// at power-on a different first entry means that the firmware did not
// keep the order through the reset, or that a person changed it in the
// firmware menu. Then init cannot be sure which entry boots after the
// requester's boot, so it refuses a handover.
func observeBootOrder(act bootActuator, stateRoot string) *bool {
	record := provenRecord(stateRoot)
	if record == nil {
		return nil
	}
	kept := act.fallbackLeads(record.Slot)
	if !kept {
		fmt.Printf("liken: system: slot %s is the proven slot in the store, but it did not lead BootOrder at power-on. "+
			"The firmware did not keep the order that the last shutdown wrote, so no reboot in this boot leaves BootNext to a requester\n", record.Slot)
	}
	return &kept
}

// handOverOneShot checks the conditions that do not depend on the
// firmware type, and then calls the actuator's handOver, which writes
// the proven slot's entries and leaves BootNext as the requester set
// it. It returns an error when the handover is refused.
func handOverOneShot(act bootActuator, stateRoot string) error {
	record := provenRecord(stateRoot)
	if record == nil {
		return errNoProvenSlot
	}
	switch {
	case bootOrderKept == nil:
		return errors.New("no check at power-on shows whether the firmware keeps BootOrder through a reset")
	case !*bootOrderKept:
		return fmt.Errorf("slot %s did not lead BootOrder at power-on, so the firmware may not keep BootOrder through a reset", record.Slot)
	}
	return act.handOver(record.Slot)
}

// errNoProvenSlot is the refusal when the store records no proven slot.
// Such a reboot writes no slot's entries, so the caller removes the
// requester's BootNext itself.
var errNoProvenSlot = errors.New("the store records no proven slot for the boot after the requester's boot to return to")

// errNoOneShot is the refusal of a machine whose firmware has no
// BootNext.
var errNoOneShot = errors.New("this machine's firmware has no BootNext to hand over")

// hasSlotLoader reports whether the slot mounted at mount has the
// loader at the firmware's default boot path.
func hasSlotLoader(mount string) bool {
	if mount == "" {
		return false
	}
	_, err := os.Stat(defaultLoaderPath(mount))
	return err == nil
}
