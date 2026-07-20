package main

// The boot actuator: the firmware interface the upgrade path uses.
//
// Blue-green upgrades need exactly three actions from the code that
// boots the machine. First, point the next boot, and only the next
// boot, at a slot on trial. Second, keep every later boot pointed at
// the proven slot. Third, report whether that standing preference is
// actually in effect. Everything else about upgrades stays the same
// on every machine and lives in proving.go: the staged/proven/
// attempted store, the verdicts, and the order of operations that
// stops a failing release from causing repeated reboots. Only these
// three actions differ by firmware. They form the whole interface
// between the lifecycle and the machine's boot hardware. Each
// firmware's implementation of the interface is called a dialect.
//
// A UEFI machine's firmware holds boot preferences itself, as
// variables in NVRAM. The UEFI specification defines the interface:
// BootNext is the one-shot trial, and BootOrder is the standing
// preference (efiactuator.go). A BIOS machine's firmware holds no
// boot preferences, so liken stores them where GRUB can read them:
// the environment block on the boot home (grubactuator.go). A
// machine chooses this path by declaring the biosBoot and bootHome
// storage roles. The installed grubenv file is the sign that the
// GRUB interface is in place.

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/machine"
)

// bootActuator lists the three actions the proving lifecycle asks of
// the firmware. Each method performs one of the three actions
// described above.
type bootActuator interface {
	// canArmTrial reports whether armTrial could point a boot at the
	// given slot. It changes nothing. It exists because of an
	// ordering rule in armProvingBoot: the code must write the
	// attempted marker before it arms the trial. A marker with no
	// trial behind it reads as "tried and fell back" on the next
	// boot. That is a false rejection of a release that never ran.
	// The code must find anything knowably wrong before it writes
	// the marker, and this method is that check.
	canArmTrial(slot string) error

	// armTrial makes the next boot, and only the next boot, try the
	// given slot. The one-shot property makes a trial safe: the boot
	// that the arming triggers also consumes it. So any reset after
	// that boot (a panic, a watchdog, a power cut) returns to the
	// standing preference. On success, armTrial returns a
	// console-ready description of what it armed, in the firmware's
	// own terms.
	armTrial(slot string) (string, error)

	// fallbackLeads reports whether the standing boot preference
	// leads with the given slot. It confirms this by reading the
	// preference back; it does not trust that an earlier write
	// worked. A trial is safe only when every reset lands on a
	// proven slot. The reboot path uses this method to check that
	// before it arms a trial.
	fallbackLeads(slot string) bool

	// assertProven makes the standing boot preference lead with the
	// given slot, and corrects any drift. It runs on every boot and
	// after every promotion. This keeps the store on disk as the
	// authority; the firmware only ever holds a copy of it.
	assertProven(slot string)
}

// chooseBootActuator picks the firmware interface this machine uses.
// It applies the same test the installer uses: /sys/firmware/efi
// exists only when UEFI booted this kernel. A BIOS machine uses
// GRUB when the boot home carries an installed environment block.
// Without one, for example on external media, a direct-kernel lab
// boot, or a machine installed without the GRUB roles, no firmware
// interface is available.
func chooseBootActuator() bootActuator {
	if firmwareIsUEFI() {
		return efiActuator{dir: efiVarsDir}
	}
	grubDir := filepath.Join(roleMounts[machine.BootHomeRole].path, "grub")
	if _, err := os.Stat(filepath.Join(grubDir, "grubenv")); err == nil {
		return grubActuator{grubDir: grubDir, machineName: bootParamValue("liken.machine")}
	}
	return noActuator{}
}

// noActuator serves a machine with no way to choose its next boot.
// BIOS firmware holds no boot variables, and a boot from external
// media or a direct-kernel QEMU boot has no standing preference to
// manage. Every assertion does nothing, and no trial can ever arm.
// A machine that cannot guarantee its fallback must stay on the
// version that works.
type noActuator struct{}

func (noActuator) canArmTrial(slot string) error {
	return errNoActuator
}

func (noActuator) armTrial(slot string) (string, error) {
	return "", errNoActuator
}

func (noActuator) fallbackLeads(slot string) bool { return false }

func (noActuator) assertProven(slot string) {}

var errNoActuator = errors.New("this machine's firmware holds no boot variables, so init has no way to arm a one-shot trial")
