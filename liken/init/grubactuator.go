package main

// grubActuator is the bootActuator (actuator.go) of a BIOS machine.
//
// BIOS firmware stores no boot variables, so a BIOS machine keeps its
// boot preferences in the GRUB environment block on the boot home
// (grubenv.go). try_slot is the one-shot trial: grub.cfg reads and
// clears it before it loads the kernel, as UEFI firmware does with
// BootNext. grub.cfg boots default_slot when try_slot is empty, as
// UEFI firmware boots the first entry of BootOrder.
//
// A BIOS machine's boot path is on its disk: the MBR boot code, GRUB's
// core image, and grub.cfg on the boot home. Some cloud hosts rewrite
// the MBR of a running machine, for example when you change a Linode's
// boot mode. So assertProven, which runs on every boot and before every
// reboot, also repairs the boot path (repairBootChain). Without that, a
// machine whose MBR was zeroed while it ran would not boot again after
// its next reboot.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/liken/machine"
)

type grubActuator struct {
	// grubDir is the boot home's grub directory, which has grub.cfg
	// and grubenv.
	grubDir string
	// machineName is the liken.machine= value of the kernel command
	// line. repairBootChain renders it into grub.cfg.
	machineName string
}

func (a grubActuator) envPath() string { return filepath.Join(a.grubDir, "grubenv") }

func (a grubActuator) canArmTrial(slot string) error {
	if _, err := readGRUBEnv(a.envPath()); err != nil {
		return fmt.Errorf("reading the GRUB environment block: %w", err)
	}
	return nil
}

func (a grubActuator) armTrial(slot string) (string, error) {
	if err := updateGRUBEnv(a.envPath(), map[string]string{"try_slot": slot}); err != nil {
		return "", fmt.Errorf("arming try_slot: %w", err)
	}
	return "try_slot=" + slot + " written to the GRUB environment block", nil
}

func (a grubActuator) fallbackLeads(slot string) bool {
	env, err := readGRUBEnv(a.envPath())
	return err == nil && env["default_slot"] == slot
}

// handOver returns errNoOneShot, because GRUB has no one-shot boot
// entry that the program that requested the reboot can set
// (handover.go). try_slot selects only a liken slot.
func (a grubActuator) handOver(slot string) error { return errNoOneShot }

// dropOneShot does nothing, because only init sets try_slot.
func (a grubActuator) dropOneShot() {}

// assertProven sets default_slot to the proven slot and then repairs
// the boot chain on disk from that slot's artifacts.
func (a grubActuator) assertProven(slot string) {
	env, err := readGRUBEnv(a.envPath())
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "liken: system: reading the GRUB environment block: %v\n", err)
	case env["default_slot"] != slot || env["try_slot"] != "":
		// Clear try_slot too, so a trial armed for a release that was
		// withdrawn does not run on a later reboot.
		if err := updateGRUBEnv(a.envPath(), map[string]string{"default_slot": slot, "try_slot": ""}); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: writing default_slot to the GRUB environment block: %v\n", err)
		} else if readback, err := readGRUBEnv(a.envPath()); err != nil || readback["default_slot"] != slot {
			// Check the value read back, not the result of the write,
			// as efiActuator does with BootOrder.
			fmt.Fprintln(os.Stderr, "liken: system: default_slot in the GRUB environment block does not read back as slot "+slot+" after the write")
		} else {
			fmt.Printf("liken: system: default_slot in the GRUB environment block is now slot %s, the proven slot\n", slot)
		}
	}

	// The repair runs even when the environment block could not be
	// read or written, because a zeroed MBR stops the machine from
	// booting whatever the block says.
	a.repairBootChain(slot)
}

// repairBootChain compares the boot sectors and grub.cfg with what the
// proven slot's artifacts and the machine name give, and rewrites only
// a part that differs. It prints nothing when all of them match.
func (a grubActuator) repairBootChain(slot string) {
	slotMount := slotMountPath(slot)
	if slotMount == "" {
		return
	}
	bootImg, err := os.ReadFile(filepath.Join(slotMount, "grub-boot.img"))
	if err != nil {
		// A release from before liken shipped GRUB artifacts has no
		// copy of the boot sectors to compare with, so leave the chain
		// that booted this machine as it is.
		fmt.Printf("liken: system: slot %s has no grub-boot.img, so init leaves the boot sectors as they are\n", slot)
		return
	}
	coreImg, err := os.ReadFile(filepath.Join(slotMount, "grub-core.img"))
	if err != nil {
		fmt.Printf("liken: system: slot %s has no grub-core.img, so init leaves the boot sectors as they are\n", slot)
		return
	}

	parts := discoverPartitions()
	biosBoot, err := findSlotPartition(parts, machine.BIOSBootRole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v, so init does not check the boot sectors\n", err)
		return
	}
	diskDev, err := diskDevice(parts, machine.BIOSBootRole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v, so init does not check the boot sectors\n", err)
		return
	}
	plan, err := planGRUBBootSectors(bootImg, coreImg, biosBoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: planning the boot sectors: %v\n", err)
		return
	}
	disk, err := os.OpenFile(diskDev, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: opening %s to check the boot sectors: %v\n", diskDev, err)
		return
	}
	defer disk.Close()
	ok, err := plan.inPlace(disk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: reading the boot sectors on %s: %v\n", diskDev, err)
		return
	}
	if !ok {
		if err := plan.write(disk); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: writing the boot sectors on %s: %v\n", diskDev, err)
			return
		}
		fmt.Printf("liken: system: the boot sectors on %s differed from the files of slot %s, so init rewrote them\n", diskDev, slot)
	}

	// grub.cfg is rendered from the machine name, not copied from the
	// slot. Without a machine name there is no correct file to render,
	// so leave it as it is.
	if a.machineName == "" {
		return
	}
	cfgPath := filepath.Join(a.grubDir, "grub.cfg")
	want := []byte(renderGRUBConfig(a.machineName, consoleArgs()))
	current, err := os.ReadFile(cfgPath)
	if err == nil && bytes.Equal(current, want) {
		return
	}
	if err := writeFileDurably(cfgPath, want); err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: writing grub.cfg: %v\n", err)
		return
	}
	fmt.Println("liken: system: grub.cfg differed from the one rendered for this machine, so init rewrote it")
}

// slotMountPath returns the mount point of slot "A" or "B" from
// roleMounts, and "" for any other value.
func slotMountPath(slot string) string {
	switch slot {
	case "A":
		return roleMounts[machine.SystemARole].path
	case "B":
		return roleMounts[machine.SystemBRole].path
	}
	return ""
}

// biosFirmwareFacts reports a BIOS machine's boot configuration from
// the GRUB environment block and the kernel command line. It fills the
// same fields as the UEFI report, so a fleet listing reads the same
// for either firmware, and each value says where it came from.
func biosFirmwareFacts() machine.FirmwareStatus {
	fw := machine.FirmwareStatus{Mode: machine.FirmwareBIOS}
	if slot := bootParamValue("liken.slot"); slot != "" {
		fw.BootCurrent = fmt.Sprintf("liken slot %s (liken.slot= on the kernel command line)", slot)
	}
	grubDir := filepath.Join(roleMounts[machine.BootHomeRole].path, "grub")
	env, err := readGRUBEnv(filepath.Join(grubDir, "grubenv"))
	if err != nil {
		return fw
	}
	if try := env["try_slot"]; try != "" {
		fw.BootNext = fmt.Sprintf("liken slot %s (grubenv try_slot)", try)
	}
	if def := env["default_slot"]; def != "" {
		fw.BootOrder = []string{fmt.Sprintf("liken slot %s (grubenv default_slot)", def)}
	}
	return fw
}
