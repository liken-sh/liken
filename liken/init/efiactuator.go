package main

// The UEFI implementation of the boot actuator (actuator.go describes
// the interface and why it exists).
//
// UEFI firmware keeps its boot preferences as variables (efi.go), and
// the specification already provides exactly the two mechanisms
// blue-green upgrades need. BootNext is a one-shot: the firmware
// consumes it as it boots, so a slot on trial gets exactly one
// chance, and any reset after that falls back to BootOrder.
// BootOrder is the standing preference list, and keeping the proven
// slot at its head is what makes the fallback real.

import (
	"fmt"
	"os"
	"path/filepath"
)

type efiActuator struct {
	// dir is the efivarfs mount, or a test's stand-in for one.
	dir string
	// machineName is rendered into every boot entry this dialect
	// writes. An entry carries identity, so an anonymous entry would be
	// wrong on every later boot. This value comes from the kernel
	// command line, where an earlier boot's entry put it.
	machineName string
	// espLink is the path that names the proven slot's ESP for a
	// requester's pod (linkProvenESP), or empty, as in a test that
	// does not read it.
	espLink string
}

func (a efiActuator) canArmTrial(slot string) error {
	if _, ok := findSlotEntry(a.dir, slot); !ok {
		return fmt.Errorf("no boot entry answers to %q", "liken slot "+slot)
	}
	return nil
}

func (a efiActuator) armTrial(slot string) (string, error) {
	entry, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return "", fmt.Errorf("no boot entry answers to %q", "liken slot "+slot)
	}
	if err := writeEFIVar(a.dir, "BootNext", bootNextPayload(entry)); err != nil {
		return "", fmt.Errorf("arming BootNext: %w", err)
	}
	return "BootNext armed at " + bootEntryID(entry), nil
}

func (a efiActuator) fallbackLeads(slot string) bool {
	leader, ok := findSlotEntry(a.dir, slot)
	if !ok {
		return false
	}
	order := readBootOrder(a.dir)
	return len(order) > 0 && order[0] == leader
}

// assertProven brings this machine's whole boot path into agreement
// with the store: NVRAM holds an entry for each slot with the proven
// slot at the head of BootOrder (bootentries.go), and the proven slot
// alone answers the firmware's default boot path (slotloader.go).
//
// Both halves are needed, and they answer different failures. The
// entries are what an ordinary boot reads. The default path is what a
// firmware falls back on when it has no entries left to read.
//
// The two assertions run separately on purpose. A slot that cannot
// carry a loader must not stop the entries from healing, and a variable
// store that refuses a write must not stop the loader from landing.
//
// The first half asserts more than the standing order: it also aims
// the one-shot BootNext at the proven slot, for the firmware that
// holds BootNext through a reset but not BootOrder (assertBootNext in
// bootentries.go).
//
// It also keeps the ESP of the next boot in order for a requester's
// firmware update: the link that names the proven slot's ESP follows
// the proven slot, and the other slot holds no staged capsule.
func (a efiActuator) assertProven(slot string) {
	healBootEntries(a.dir, a.machineName, slot, true)
	a.healSlotLoader(slot)
	a.tendESPs(slot)
}

// handOver asserts the proven slot as assertProven does, except for
// BootNext, which stays as the requester set it. It refuses, before it
// writes anything, a BootNext that names the other slot: that would
// boot a release that is not proven, outside the trial that proves
// one. It refuses after the heal when the proven slot holds no
// fallback loader, because the heal is what writes that loader, and the
// caller's ordinary assertion then takes the one shot back.
func (a efiActuator) handOver(slot string) error {
	if other, ok := findSlotEntry(a.dir, otherSlot(slot)); ok {
		if next, err := readEFIVar(a.dir, "BootNext"); err == nil && bootNextEntry(next) == int32(other) {
			return fmt.Errorf("BootNext names %s, which is not the proven slot", bootEntryID(other))
		}
	}
	healBootEntries(a.dir, a.machineName, slot, false)
	a.healSlotLoader(slot)
	a.tendESPs(slot)
	if !hasSlotLoader(slotMountPath(slot)) {
		return fmt.Errorf("slot %s holds no fallback loader for a firmware that loses its boot entries", slot)
	}
	return nil
}

// healSlotLoader keeps the fallback loader on the proven slot and
// nowhere else. A firmware at its defaults takes the first answer it
// finds, and the other slot holds an older release or nothing at all.
//
// The order of these two steps is the whole guarantee. The proven slot
// takes the loader first, and only a slot that took it lets the other
// slot lose its own. A machine is never left with neither.
func (a efiActuator) healSlotLoader(slot string) {
	mount := slotMountPath(slot)
	if mount == "" || a.machineName == "" {
		// A slot that is not mounted cannot take a loader, and a boot
		// with no name has nothing correct to write in the loader's
		// entry. Both cases leave the loader that is already there.
		return
	}
	if err := writeSlotLoader(mount, slot, a.machineName); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v; this machine has no fallback for a firmware that loses its boot entries\n", err)
		return
	}
	if other := slotMountPath(otherSlot(slot)); other != "" {
		removeSlotLoader(other)
	}
}

// dropOneShot removes BootNext, so the reboot follows BootOrder.
func (a efiActuator) dropOneShot() {
	if err := removeEFIVar(a.dir, "BootNext"); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: removing BootNext: %v; the requester's boot may run\n", err)
	}
}

// tendESPs keeps the two slots' ESPs in order for a firmware update.
// A requester's pod writes fwupd's updater and capsule to the ESP that
// the next boot reads. That is the proven slot's, because the reboot
// that hands BootNext to a requester arms no trial, and the operator
// downloads each release to the other slot, which would overwrite the
// files. So init keeps one link that names the proven slot's ESP. A
// capsule in \EFI\UpdateCapsule on the other slot would apply at a
// later boot from that slot, a firmware change that nobody asked for,
// so init removes the directory there.
func (a efiActuator) tendESPs(slot string) {
	if mount := slotMountPath(slot); mount != "" && a.espLink != "" {
		if err := linkProvenESP(a.espLink, mount); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
		}
	}
	if other := slotMountPath(otherSlot(slot)); other != "" {
		removeStagedCapsules(other)
	}
}

// linkProvenESP points link at the proven slot's mount. It replaces
// the link with a rename, so a pod that starts while the proven slot
// moves mounts one slot or the other, never a missing path. The first
// assertion of a boot runs before init creates the rest of /run/liken,
// so this creates the link's directory.
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

// removeStagedCapsules deletes the capsules that wait on a slot's ESP
// for the firmware. The firmware reads \EFI\UpdateCapsule on the ESP it
// boots from.
func removeStagedCapsules(mount string) {
	dir := filepath.Join(mount, "EFI", "UpdateCapsule")
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: removing the staged capsules at %s: %v\n", dir, err)
		return
	}
	if err := flushSlot(mount); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v\n", err)
	}
	fmt.Printf("liken: system: removed the capsules staged at %s, on the slot that is not proven\n", dir)
}
