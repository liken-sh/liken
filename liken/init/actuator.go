package main

// A bootActuator sets which slot the machine boots, and it is the only
// part of an upgrade that differs by firmware. An upgrade needs it to
// boot a trial slot on the next boot only, to boot the proven slot on
// every boot after that, and to report whether the firmware still
// prefers the proven slot. The rest of the upgrade is the same on every
// machine and is in proving.go.
//
// A UEFI machine keeps its boot preferences in NVRAM variables:
// BootNext for the trial and BootOrder for the proven slot
// (efiactuator.go). A BIOS machine has no such variables, so liken
// keeps them in GRUB's environment block on the boot home
// (grubactuator.go). A BIOS machine has a grubenv file only when its
// manifest declares the biosBoot and bootHome storage roles.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/liken-sh/liken/liken/machine"
)

// firmwareWrites serializes the reboot path and the proving watch,
// which can write the firmware's boot settings at the same time. The
// reboot path sets the proven slot and then arms the trial. When the
// watch writes between or after those, the firmware can skip the
// trial while the attempted marker stays set, and the next boot reads
// that as a release that ran and failed.
//
// Every holder unlocks with defer. The reboot path can run inside a
// machine-plane component, which the plane restarts after a panic. A
// lock left held blocks every later reboot, and only a power cycle
// recovers the machine.
var firmwareWrites sync.Mutex

// shuttingDown is set by the reboot path before it takes
// firmwareWrites, and is never cleared. The proving watch checks it
// under the lock, so a watch that gets the lock after the reboot path
// leaves the armed trial alone.
var shuttingDown atomic.Bool

type bootActuator interface {
	// canArmTrial reports whether armTrial would succeed, and changes
	// nothing. armProvingBoot writes the attempted marker before it
	// arms the trial, so it calls canArmTrial first. A marker with no
	// trial would reject a release that never ran.
	canArmTrial(slot string) error

	// armTrial makes only the next boot use the given slot. That boot
	// clears the setting, so any later reset, such as a panic, a
	// watchdog, or a power cut, boots the proven slot. It returns a
	// description of the setting it wrote, for the console.
	armTrial(slot string) (string, error)

	// fallbackLeads reports whether the firmware's boot order starts
	// with the given slot. It reads the firmware, and does not trust an
	// earlier write. A trial is safe only when every reset after it
	// boots the proven slot.
	fallbackLeads(slot string) bool

	// assertProven makes the boot order start with the given slot. It
	// runs on every boot and after every promotion, because the store
	// on disk is the source of truth and the firmware has a copy. It
	// also repairs any part of the boot path that can be damaged while
	// the machine runs, so that the next reboot comes back.
	assertProven(slot string)

	// handOver is assertProven for a reboot whose one-shot boot entry a
	// Disruption set (handover.go). It leaves that entry in place. It
	// returns an error when the boot after that one might not return to
	// the proven slot. When BootNext already points at the other slot,
	// it returns before it writes anything. When the proven slot has no
	// fallback loader, it returns the error after it has repaired the
	// boot entries.
	handOver(slot string) error

	// dropOneShot removes a Disruption's one-shot entry after init
	// refuses the handover on a machine with no proven slot. Such a
	// machine never runs assertProven, so without this the refused
	// boot would still run.
	dropOneShot()
}

// chooseBootActuator returns noActuator on a BIOS machine with no
// grubenv file: a boot from external media, a direct-kernel QEMU boot,
// or a machine installed without the GRUB roles.
func chooseBootActuator() bootActuator {
	if firmwareIsUEFI() {
		return efiActuator{dir: efiVarsDir, machineName: bootParamValue("liken.machine"), espLink: machine.ProvenESPLink}
	}
	grubDir := filepath.Join(roleMounts[machine.BootHomeRole].path, "grub")
	if _, err := os.Stat(filepath.Join(grubDir, "grubenv")); err == nil {
		return grubActuator{grubDir: grubDir, machineName: bootParamValue("liken.machine")}
	}
	return noActuator{}
}

// noActuator arms no trial, because a machine that cannot boot the
// proven slot after a failed trial must stay on the release it runs.
type noActuator struct{}

func (noActuator) canArmTrial(slot string) error {
	return errNoActuator
}

func (noActuator) armTrial(slot string) (string, error) {
	return "", errNoActuator
}

func (noActuator) fallbackLeads(slot string) bool { return false }

func (noActuator) assertProven(slot string) {}

func (noActuator) handOver(slot string) error { return errNoOneShot }

func (noActuator) dropOneShot() {}

var errNoActuator = errors.New("this machine has no UEFI boot variables and no grubenv file, so init cannot arm a one-shot trial boot")
