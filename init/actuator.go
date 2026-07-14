package main

// The boot actuator: the firmware dialect the upgrade path speaks.
//
// Blue-green upgrades need exactly three things from whatever boots
// the machine: point the next boot, and only the next, at a slot on
// trial; keep every boot after that pointed at the proven slot; and
// tell whether that standing preference actually holds. Everything
// else about upgrades — the staged/proven/attempted store, the
// verdicts, the ordering that keeps a failing release from reboot-
// looping — is the same on every machine, and lives in proving.go.
// Only these three acts differ by firmware, so they are the whole
// interface between the lifecycle and the machine's boot hardware.
//
// A UEFI machine's firmware holds boot preferences itself, as
// variables in NVRAM, and the dialect is written into the UEFI
// specification: BootNext is the one-shot trial, BootOrder is the
// standing preference (efiactuator.go).

import "errors"

// bootActuator is what the proving lifecycle asks of the firmware;
// each method is one of the three acts described above.
type bootActuator interface {
	// canArmTrial reports whether armTrial could point a boot at the
	// given slot, without changing anything. It exists because of an
	// ordering constraint in armProvingBoot: the attempted marker
	// must be written before the trial is armed, and a marker with
	// no trial behind it reads as "tried and fell back" on the next
	// boot — a false rejection of a release that never ran. Anything
	// knowably wrong must be discovered before the marker, and this
	// is that check.
	canArmTrial(slot string) error

	// armTrial makes the next boot, and only the next, try the given
	// slot. The one-shot property is what makes a trial safe: the
	// arming is consumed by the boot it triggers, so any reset after
	// it (a panic, a watchdog, a power cut) lands back on the
	// standing preference. On success it returns a console-ready
	// description of what was armed, in the dialect's own terms.
	armTrial(slot string) (string, error)

	// fallbackLeads reports whether the standing boot preference is
	// verified to lead with the given slot — by reading it back, not
	// by trusting that an earlier write worked. A trial is only safe
	// when every reset lands on a proven slot, and this is how the
	// reboot path checks that before arming one.
	fallbackLeads(slot string) bool

	// assertProven makes the standing boot preference lead with the
	// given slot, correcting whatever drifted. It runs on every boot
	// and after every promotion, so the store on disk stays the
	// authority and the firmware only ever holds a copy.
	assertProven(slot string)
}

// chooseBootActuator picks the dialect this machine's firmware
// speaks. The regime test is the same one the installer uses:
// /sys/firmware/efi exists exactly when UEFI booted this kernel.
func chooseBootActuator() bootActuator {
	if firmwareIsUEFI() {
		return efiActuator{dir: efiVarsDir}
	}
	return noActuator{}
}

// noActuator is the dialect of a machine with no way to choose its
// next boot: BIOS firmware holds no boot variables, and a boot from
// external media or a direct-kernel QEMU boot has no standing
// preference to manage. Every assertion is a quiet no-op, and no
// trial can ever arm — a machine that cannot guarantee its fallback
// must stay on the version that works.
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
