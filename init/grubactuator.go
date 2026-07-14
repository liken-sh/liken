package main

// The GRUB dialect of the boot actuator (actuator.go describes the
// interface and why it exists).
//
// BIOS firmware holds no boot variables, so a BIOS machine's boot
// preferences live where GRUB can read them: the environment block
// on the boot home (grubenv.go describes the format). The dialect
// maps one-to-one onto UEFI's: try_slot is the one-shot trial
// (grub.cfg consumes it before loading a single kernel byte, exactly
// as firmware consumes BootNext), and default_slot is the standing
// preference (the stand-in for BootOrder's head).
//
// This dialect has one duty UEFI never needed: healing. A UEFI
// machine's boot path lives in NVRAM, which nothing but the firmware
// touches. A BIOS machine's boot path lives on the disk itself — the
// MBR's boot code, GRUB's core image, the config on the boot home —
// and cloud hosts are known to rewrite MBRs under running machines
// (Linode's boot-mode changes do exactly that). So asserting the
// proven slot here also re-derives every byte of the boot chain from
// the proven slot's own artifacts and puts back whatever disagrees.
// It runs on every boot and on the way down before every reboot,
// because a boot path zeroed while the machine runs must be healed
// before the reboot that would otherwise never come back.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liken-sh/liken/machine"
)

type grubActuator struct {
	// grubDir is the boot home's grub directory, holding grub.cfg
	// and grubenv.
	grubDir string
	// machineName is rendered into grub.cfg when healing it; it
	// comes from the kernel command line, where GRUB's own config
	// put it.
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
// the store: the environment block prefers the proven slot, and the
// boot chain on disk matches the proven slot's own artifacts.
func (a grubActuator) assertProven(slot string) {
	env, err := readGRUBEnv(a.envPath())
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "liken: system: reading the GRUB environment block: %v\n", err)
	case env["default_slot"] != slot || env["try_slot"] != "":
		// A leftover try_slot is cleared along with the preference
		// write: a one-shot that was armed for a release since
		// withdrawn must not fire on some later reboot.
		if err := updateGRUBEnv(a.envPath(), map[string]string{"default_slot": slot, "try_slot": ""}); err != nil {
			fmt.Fprintf(os.Stderr, "liken: system: asserting default_slot in the GRUB environment block: %v\n", err)
		} else if readback, err := readGRUBEnv(a.envPath()); err != nil || readback["default_slot"] != slot {
			// Trust the readback, not the write, exactly as the UEFI
			// dialect does with BootOrder.
			fmt.Fprintln(os.Stderr, "liken: system: the GRUB environment block was written but reads back unchanged")
		} else {
			fmt.Printf("liken: system: the GRUB environment block now prefers slot %s (proven)\n", slot)
		}
	}

	// The environment block and the boot chain are separate
	// assertions on purpose: a torn environment block must not stop
	// the boot sectors from healing, or the other way around.
	a.healBootChain(slot)
}

// healBootChain re-derives the on-disk boot chain from the proven
// slot's artifacts and rewrites whatever disagrees. The comparison
// runs every time; the writes only when something drifted, so a
// healthy machine's console says nothing.
func (a grubActuator) healBootChain(slot string) {
	slotMount := slotMountPath(slot)
	if slotMount == "" {
		return
	}
	bootImg, err := os.ReadFile(filepath.Join(slotMount, "grub-boot.img"))
	if err != nil {
		// A proven release from before liken carried GRUB artifacts
		// can't say what the boot sectors should hold; the chain that
		// booted this machine stays as it is.
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
	// re-render and compare. Without a machine name there is nothing
	// correct to render, so the file is left alone rather than
	// anonymized.
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

// slotMountPath translates a slot letter to its mountpoint via the
// same table storage reconciliation mounts by, so tests can stand in
// a tempdir the way they do for every role.
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
// the kernel command line GRUB composed. The fields deliberately
// mirror the UEFI report — a fleet listing reads the same either way,
// with each fact naming the mechanism it came from.
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
