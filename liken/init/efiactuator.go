package main

// The UEFI implementation of bootActuator (actuator.go).
//
// UEFI firmware stores its boot preferences as variables (efi.go).
// BootNext names the entry for the next boot only: the firmware
// deletes it as it boots, so a slot on trial gets one boot, and any
// reset after that follows BootOrder. The proven slot stays first in
// BootOrder, so the machine falls back to it.

import (
	"fmt"
	"os"
	"path/filepath"
)

type efiActuator struct {
	// dir is the efivarfs mount, or a test's stand-in for one.
	dir string
	// machineName is the liken.machine kernel parameter. Every boot
	// entry passes it to the next boot.
	machineName string
	// espLink is the path of the symlink to the proven slot's ESP. A
	// pod that stages a firmware capsule, such as firmware-operator's,
	// mounts it. When it is empty, no link is written.
	espLink string
}

// canArmTrial returns an error when the slot has no boot entry,
// because armTrial needs that entry to set BootNext.
func (a efiActuator) canArmTrial(slot string) error {
	if _, ok := findSlotEntry(a.dir, slot); !ok {
		return fmt.Errorf("no boot entry has the description %q", "liken slot "+slot)
	}
	return nil
}

// armTrial sets BootNext to the slot's boot entry. The firmware
// deletes BootNext when it boots that entry, so a reset after the trial
// boot follows BootOrder to the proven slot.
func (a efiActuator) armTrial(slot string) (string, error) {
	entry, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return "", fmt.Errorf("no boot entry has the description %q", "liken slot "+slot)
	}
	if err := writeEFIVar(a.dir, "BootNext", bootNextPayload(entry)); err != nil {
		return "", fmt.Errorf("arming BootNext: %w", err)
	}
	return "BootNext armed at " + bootEntryID(entry), nil
}

// fallbackLeads reports whether the slot's boot entry is first in
// BootOrder.
func (a efiActuator) fallbackLeads(slot string) bool {
	leader, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return false
	}
	order := readBootOrder(a.dir)
	return len(order) > 0 && order[0] == leader
}

// assertProven restores the boot path to the proven slot. It writes a
// boot entry for each slot with the proven slot first in BootOrder,
// and sets BootNext to the proven slot (bootentries.go). It puts the
// fallback loader at the firmware's default path on the proven slot
// only (slotloader.go). A normal boot reads the entries, and a
// firmware that has lost its entries boots the default path. The steps
// run independently, so a failed NVRAM write does not stop the loader
// from being written, and a slot that cannot be written does not stop
// the entries.
func (a efiActuator) assertProven(slot string) {
	repairBootEntries(a.dir, a.machineName, slot, true)
	a.writeProvenSlotLoader(slot)
	a.updateESPs(slot)
}

// handOver does what assertProven does, except that it leaves
// BootNext as the pod that requested the reboot set it (handover.go).
// Before it writes anything, it
// returns an error when BootNext names the other slot, because that
// would boot a release that no trial has proven. After the writes, it
// returns an error when the proven slot has no fallback loader. On an
// error the caller runs assertProven, which replaces BootNext.
func (a efiActuator) handOver(slot string) error {
	if other, ok := findSlotEntry(a.dir, otherSlot(slot)); ok {
		if next, err := readEFIVar(a.dir, "BootNext"); err == nil && bootNextEntry(next) == int32(other) {
			return fmt.Errorf("BootNext is set to %s, which is not the proven slot", bootEntryID(other))
		}
	}
	repairBootEntries(a.dir, a.machineName, slot, false)
	a.writeProvenSlotLoader(slot)
	a.updateESPs(slot)
	if !hasSlotLoader(slotMountPath(slot)) {
		return fmt.Errorf("slot %s has no fallback loader, so a firmware that loses its boot entries cannot boot it", slot)
	}
	return nil
}

// writeProvenSlotLoader puts the fallback loader on the proven slot
// and removes it from the other slot, which holds an older release or
// nothing. A firmware with no entries boots the first loader it finds
// at the default path. The removal runs only after the write
// succeeds, so the machine always has one fallback loader.
func (a efiActuator) writeProvenSlotLoader(slot string) {
	mount := slotMountPath(slot)
	if mount == "" || a.machineName == "" {
		// Without a mount or a machine name, no correct loader can be
		// written, so the existing one stays.
		return
	}
	if err := writeSlotLoader(mount, slot, a.machineName); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v. The fallback loader is the only way to boot this machine when the firmware loses its boot entries\n", err)
		return
	}
	if other := slotMountPath(otherSlot(slot)); other != "" {
		removeSlotLoader(other)
	}
}

// dropOneShot removes BootNext, so the next boot follows BootOrder.
func (a efiActuator) dropOneShot() {
	if err := removeEFIVar(a.dir, "BootNext"); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: removing BootNext: %v. The next boot can run the entry that BootNext names\n", err)
	}
}

// updateESPs points espLink at the proven slot's ESP and deletes the
// capsules staged on the other slot's ESP. A pod that stages a firmware
// update writes fwupd's updater and capsule through espLink. The files
// must be on the proven slot's ESP, because init arms no trial on a
// reboot that leaves BootNext to fwupd, so that boot reads the proven
// slot. The other slot is also where the operator downloads each
// release, which could overwrite the files. A capsule left on the
// other slot would install when the machine later boots that slot, and
// nobody requested that firmware update.
func (a efiActuator) updateESPs(slot string) {
	if mount := slotMountPath(slot); mount != "" && a.espLink != "" {
		if err := linkProvenESP(a.espLink, mount); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
		}
	}
	if other := slotMountPath(otherSlot(slot)); other != "" {
		removeStagedCapsules(other)
	}
}

// linkProvenESP replaces link with a rename, so a pod that starts
// while the proven slot changes finds one slot or the other, never a
// missing path. It creates the link's directory, because the first
// assertProven of a boot runs before init creates the rest of
// /run/liken.
func linkProvenESP(link, mount string) error {
	if current, err := os.Readlink(link); err == nil && current == mount {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("linking %s to the proven slot's ESP: %w", link, err)
	}
	next := link + ".next"
	_ = os.Remove(next)
	if err := os.Symlink(mount, next); err != nil {
		return fmt.Errorf("linking %s to the proven slot's ESP: %w", link, err)
	}
	if err := os.Rename(next, link); err != nil {
		return fmt.Errorf("linking %s to the proven slot's ESP: %w", link, err)
	}
	return nil
}

// removeStagedCapsules deletes \EFI\UpdateCapsule on a slot's ESP. The
// firmware installs the capsules there when it boots from that ESP.
func removeStagedCapsules(mount string) {
	dir := filepath.Join(mount, "EFI", "UpdateCapsule")
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: removing the staged capsules in %s: %v\n", dir, err)
		return
	}
	if err := flushSlot(mount); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
	}
	fmt.Printf("liken: system: removed the capsules in %s, on the slot that is not proven\n", dir)
}
