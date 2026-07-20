package main

// The GRUB dialect of the boot actuator (actuator.go describes the
// interface and explains why it exists).
//
// BIOS firmware holds no boot variables, so a BIOS machine keeps its
// boot preferences where GRUB can read them: the environment block on
// the boot home (grubenv.go describes the format). This dialect maps
// one to one onto the UEFI dialect. try_slot is the one-shot trial
// (grub.cfg consumes it before it loads a single kernel byte, in the
// same way that firmware consumes BootNext). default_slot is the
// standing preference, and it corresponds to the first entry in
// BootOrder.
//
// This dialect has one duty that UEFI never needed: healing. A UEFI
// machine's boot path lives in NVRAM, and nothing but the firmware
// touches NVRAM. A BIOS machine's boot path lives on the disk itself:
// the MBR's boot code, GRUB's core image, and the config on the boot
// home. Cloud hosts are known to rewrite MBRs under running machines
// (Linode's boot-mode changes do exactly that). So, when this dialect
// asserts the proven slot, it also re-derives every byte of the boot
// chain from the proven slot's own artifacts, and it puts back
// whatever disagrees. This healing runs on every boot and on the way
// down before every reboot, because a boot path that is zeroed while
// the machine runs must be healed before the reboot. Otherwise, the
// machine would never come back.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/machine"
)

type grubActuator struct {
	// grubDir is the boot home's grub directory. It holds grub.cfg
	// and grubenv.
	grubDir string
	// machineName is rendered into grub.cfg when this code heals
	// grub.cfg. This value comes from the kernel command line, where
	// GRUB's own config put it.
	machineName string
}

func (a grubActuator) envPath() string { return filepath.Join(a.grubDir, "grubenv") }

func (a grubActuator) canArmTrial(slot string) error {
	if _, err := readGRUBEnv(a.envPath()); err != nil {
		return fmt.Errorf("the GRUB environment block is not usable: %w", err)
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

// assertProven brings the whole GRUB boot path into agreement with
// the store. The environment block prefers the proven slot, and the
// boot chain on disk matches the proven slot's own artifacts.
func (a grubActuator) assertProven(slot string) {
	env, err := readGRUBEnv(a.envPath())
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "liken: system: reading the GRUB environment block: %v\n", err)
	case env["default_slot"] != slot || env["try_slot"] != "":
		// This call clears a leftover try_slot along with the
		// preference write. A one-shot trial that was armed for a
		// release since withdrawn must not run on a later reboot.
		if err := updateGRUBEnv(a.envPath(), map[string]string{"default_slot": slot, "try_slot": ""}); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: asserting default_slot in the GRUB environment block: %v\n", err)
		} else if readback, err := readGRUBEnv(a.envPath()); err != nil || readback["default_slot"] != slot {
			// This code trusts the readback, not the write, in the
			// same way that the UEFI dialect trusts the readback of
			// BootOrder.
			fmt.Fprintln(os.Stderr, "liken: system: the GRUB environment block was written but reads back unchanged")
		} else {
			fmt.Printf("liken: system: the GRUB environment block now prefers slot %s (proven)\n", slot)
		}
	}

	// The environment block and the boot chain are separate
	// assertions on purpose. A torn environment block must not stop
	// the boot sectors from healing, and a problem in the boot
	// sectors must not stop the environment block assertion.
	a.healBootChain(slot)
}

// healBootChain re-derives the on-disk boot chain from the proven
// slot's artifacts and rewrites whatever disagrees. The comparison
// runs every time. The write happens only when something drifted, so
// a healthy machine's console shows no message.
func (a grubActuator) healBootChain(slot string) {
	slotMount := slotMountPath(slot)
	if slotMount == "" {
		return
	}
	bootImg, err := os.ReadFile(filepath.Join(slotMount, "grub-boot.img"))
	if err != nil {
		// A proven release from before liken carried GRUB artifacts
		// cannot say what the boot sectors should hold. The chain
		// that booted this machine stays as it is.
		fmt.Printf("liken: system: slot %s carries no grub-boot.img; leaving the boot sectors alone\n", slot)
		return
	}
	coreImg, err := os.ReadFile(filepath.Join(slotMount, "grub-core.img"))
	if err != nil {
		fmt.Printf("liken: system: slot %s carries no grub-core.img; leaving the boot sectors alone\n", slot)
		return
	}

	parts := discoverPartitions()
	biosBoot, err := findSlotPartition(parts, machine.BIOSBootRole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v; the boot sectors cannot be checked\n", err)
		return
	}
	diskDev, err := diskDevice(parts, machine.BIOSBootRole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: system: %v; the boot sectors cannot be checked\n", err)
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
			fmt.Fprintf(os.Stderr, "liken: system: healing the boot sectors on %s: %v\n", diskDev, err)
			return
		}
		fmt.Printf("liken: system: the boot sectors on %s disagreed with slot %s's artifacts; healed\n", diskDev, slot)
	}

	// grub.cfg is rendered, not copied, so it heals the same way:
	// this code re-renders it and compares the result. Without a
	// machine name, there is nothing correct to render, so this code
	// leaves the file alone rather than write an anonymous version.
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
		fmt.Fprintf(os.Stderr, "liken: system: healing grub.cfg: %v\n", err)
		return
	}
	fmt.Println("liken: system: grub.cfg disagreed with this machine's rendering; healed")
}

// slotMountPath translates a slot letter to its mountpoint. It uses
// the same table that storage reconciliation uses for mounts, so
// tests can substitute a temporary directory, the same way tests do
// for every role.
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
// where that machine actually keeps it: GRUB's environment block and
// the kernel command line that GRUB composed. The fields deliberately
// mirror the UEFI report. A fleet listing reads the same way for
// either firmware type, and each fact names the mechanism it came
// from.
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
