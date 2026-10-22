package main

// The handover of the one-shot: a reboot whose BootNext belongs to a
// requester instead of to init.
//
// Every reboot path aims BootNext at the proven slot, or at a staged
// release's trial (proving.go). A UEFI capsule needs the opposite.
// fwupd sets BootNext to its own updater, the firmware boots the
// updater once, the updater hands the capsule to the firmware, and the
// firmware applies it and resets. So a requester that set BootNext in
// its turn, such as firmware-operator after it staged a capsule, says
// so with spec.keepBootNext on its Disruption, and machine-operator
// names the request in the reboot intent. On that reboot init leaves
// BootNext as it finds it, and arms no trial: a staged release stays
// staged, and its own reboot arms it later. Everything else that the
// reboot path asserts still runs, so the slots' entries and BootOrder
// are healed and the proven slot keeps its fallback loader.
//
// After the requester's boot, the firmware boots the head of BootOrder,
// with no one-shot left to steer it. So init hands the one-shot over
// only when the head of BootOrder is known to bring the machine back:
// the store names a proven slot, the firmware kept the proven slot at
// the head of BootOrder through the last reset, BootNext does not name
// the other slot, and the proven slot holds the fallback loader that a
// firmware at its defaults finds. A firmware update cannot be undone,
// so these checks are stricter than a trial's. When one fails, init
// prints why and reboots as it always does, so the requester's next
// scan finds that its update did not run.

import (
	"errors"
	"fmt"
	"os"
)

// bootOrderKept is what this boot found at power-on, before init wrote
// anything: true when the proven slot led BootOrder, false when another
// entry did, and nil when the boot could not tell, as on a machine
// whose store names no proven slot. main sets it once, from
// observeBootOrder.
var bootOrderKept *bool

// observeBootOrder reads whether the proven slot leads BootOrder. Every
// shutdown asserts the proven slot at the head, so at power-on the
// answer says whether the firmware kept that order through the reset.
// A mismatch can also mean that a person changed the order in the
// firmware's menu. Either way, the head of BootOrder is not under
// init's control, and that is what a handover depends on.
func observeBootOrder(act bootActuator, stateRoot string) *bool {
	record := provenRecord(stateRoot)
	if record == nil {
		return nil
	}
	kept := act.fallbackLeads(record.Slot)
	if !kept {
		fmt.Printf("liken: system: slot %s, which the store calls proven, did not lead BootOrder at power-on; "+
			"the firmware did not keep the order that the last shutdown wrote, so no reboot this boot hands BootNext to a requester\n", record.Slot)
	}
	return &kept
}

// handOverOneShot runs the checks that do not depend on the firmware's
// dialect, and then asks the actuator to assert the proven slot while
// it leaves BootNext to the requester.
func handOverOneShot(act bootActuator, stateRoot string) error {
	record := provenRecord(stateRoot)
	if record == nil {
		return errNoProvenSlot
	}
	switch {
	case bootOrderKept == nil:
		return errors.New("this boot could not tell whether the firmware keeps BootOrder through a reset")
	case !*bootOrderKept:
		return fmt.Errorf("slot %s did not lead BootOrder at power-on, so the firmware may not keep BootOrder through a reset", record.Slot)
	}
	return act.handOver(record.Slot)
}

// errNoProvenSlot is the refusal on a machine whose store names no
// proven slot. Its reboot pins no slot, so the caller removes the
// requester's one-shot itself.
var errNoProvenSlot = errors.New("the store names no proven slot for the boot after the requester's to come back to")

// errNoOneShot is the refusal of a firmware with no BootNext.
var errNoOneShot = errors.New("this machine's firmware has no BootNext to hand over")

// hasSlotLoader reports whether a slot answers the firmware's default
// boot path.
func hasSlotLoader(mount string) bool {
	if mount == "" {
		return false
	}
	_, err := os.Stat(defaultLoaderPath(mount))
	return err == nil
}
