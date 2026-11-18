package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/disks"
	"github.com/liken-sh/liken/liken/machine"
)

// installedGRUBHome creates a boot home with a grubenv that has vars,
// and returns an actuator for it.
func installedGRUBHome(t *testing.T, vars map[string]string) grubActuator {
	t.Helper()
	home := fakeBootHomeMount(t)
	grubDir := filepath.Join(home, "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		t.Fatalf("creating the grub directory: %v", err)
	}
	env, err := renderGRUBEnv(vars)
	if err != nil {
		t.Fatalf("rendering the GRUB environment block: %v", err)
	}
	if err := os.WriteFile(filepath.Join(grubDir, "grubenv"), env, 0o644); err != nil {
		t.Fatalf("writing grubenv: %v", err)
	}
	return grubActuator{grubDir: grubDir, machineName: "node-1"}
}

func TestGRUBActuatorArmsTheTrial(t *testing.T) {
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	armed, err := act.armTrial("B")
	if err != nil {
		t.Fatalf("arming the trial: %v", err)
	}
	if !strings.Contains(armed, "try_slot=B") {
		t.Errorf("armTrial returned %q, want a description with try_slot=B", armed)
	}
	env, err := readGRUBEnv(act.envPath())
	if err != nil {
		t.Fatalf("reading the GRUB environment block: %v", err)
	}
	if env["try_slot"] != "B" {
		t.Errorf("the environment block is %v, want try_slot=B", env)
	}
	if env["default_slot"] != "A" {
		t.Errorf("the environment block is %v, want default_slot=A unchanged", env)
	}
}

func TestGRUBActuatorRefusesToArmWithoutAnEnvBlock(t *testing.T) {
	home := fakeBootHomeMount(t)
	act := grubActuator{grubDir: filepath.Join(home, "grub")}
	if err := act.canArmTrial("B"); err == nil {
		t.Error("canArmTrial returned nil with no environment block, want an error")
	}
}

func TestGRUBActuatorReadsTheFallbackBack(t *testing.T) {
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})
	if !act.fallbackLeads("A") {
		t.Error("fallbackLeads(\"A\") is false, want true with default_slot=A")
	}
	if act.fallbackLeads("B") {
		t.Error("fallbackLeads(\"B\") is true, want false with default_slot=A")
	}
}

func TestGRUBActuatorAssertProvenFlipsTheDefaultAndClearsTheTrial(t *testing.T) {
	// The slot has no GRUB artifacts, so only grubenv changes.
	installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": "B"})

	act.assertProven("B")

	env, err := readGRUBEnv(act.envPath())
	if err != nil {
		t.Fatalf("reading the GRUB environment block: %v", err)
	}
	if env["default_slot"] != "B" {
		t.Errorf("the environment block is %v, want default_slot=B", env)
	}
	if env["try_slot"] != "" {
		t.Errorf("the environment block is %v, want try_slot cleared", env)
	}
}

func TestGRUBActuatorAssertProvenIsIdempotent(t *testing.T) {
	installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")
	before, err := os.ReadFile(act.envPath())
	if err != nil {
		t.Fatalf("reading grubenv after the first assertProven: %v", err)
	}
	act.assertProven("A")
	after, err := os.ReadFile(act.envPath())
	if err != nil {
		t.Fatalf("reading grubenv after the second assertProven: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the second assertProven rewrote grubenv, want it unchanged")
	}
}

// grubArtifactsOnSlot writes grub-boot.img and grub-core.img into the
// mount of slot A, in the same shapes as fakePayload, and returns the
// mount and the two images.
func grubArtifactsOnSlot(t *testing.T) (mount string, bootImg, coreImg []byte) {
	t.Helper()
	mount = fakeSlotAMount(t)
	bootImg = bytes.Repeat([]byte{0xB0}, disks.SectorSize)
	coreImg = bytes.Repeat([]byte{0xC0}, 3*disks.SectorSize)
	binary.LittleEndian.PutUint16(coreImg[grubBlocklistSegment:], grubLoadSegment)
	if err := os.WriteFile(filepath.Join(mount, "grub-boot.img"), bootImg, 0o644); err != nil {
		t.Fatalf("writing grub-boot.img: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mount, "grub-core.img"), coreImg, 0o644); err != nil {
		t.Fatalf("writing grub-core.img: %v", err)
	}
	return mount, bootImg, coreImg
}

func TestGRUBActuatorRepairsTheBootSectors(t *testing.T) {
	// The boot sectors of the fake disk start as zeroes, as a host
	// leaves them when it rewrites the MBR of a running machine.
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk: %v", err)
	}
	if disk[0] != 0xB0 {
		t.Error("the first byte of the MBR is not 0xB0, want the boot code of grub-boot.img")
	}
	if got := binary.LittleEndian.Uint64(disk[grubKernelSectorOffset:]); got != 2048 {
		t.Errorf("the patched MBR points at sector %d, want 2048, the biosBoot partition", got)
	}
	if disk[446+4] != 0xEE || disk[510] != 0x55 || disk[511] != 0xAA {
		t.Error("the protective MBR changed, want it unchanged")
	}
	core := disk[2048*disks.SectorSize:]
	if core[0] != 0xC0 {
		t.Error("the first byte of the core image is not 0xC0, want the contents of grub-core.img")
	}
	if got := binary.LittleEndian.Uint64(core[grubBlocklistStart:]); got != 2049 {
		t.Errorf("the blocklist of the core image starts at sector %d, want 2049, its continuation", got)
	}

	cfg, err := os.ReadFile(filepath.Join(act.grubDir, "grub.cfg"))
	if err != nil {
		t.Fatalf("reading grub.cfg: %v", err)
	}
	if !strings.Contains(string(cfg), "liken.machine=node-1") {
		t.Error("grub.cfg has no liken.machine=node-1, want the machine name rendered into it")
	}
}

func TestGRUBActuatorRepairIsQuietWhenTheChainAgrees(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")
	before, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk before the repair: %v", err)
	}
	act.assertProven("A")
	after, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk after the repair: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the disk changed, want a matching boot chain left unwritten")
	}
}

func TestGRUBActuatorRepairSkipsASlotWithoutTheArtifacts(t *testing.T) {
	// A release from before liken shipped GRUB artifacts has nothing
	// to compare the boot sectors with.
	_, dev := installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk: %v", err)
	}
	if disk[0] != 0 {
		t.Error("the boot code changed, want it unchanged when the slot has no GRUB artifacts")
	}
}

func TestChooseBootActuatorChoosesGRUBOnlyWhenGrubenvExists(t *testing.T) {
	fakeBIOSFirmware(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
	if _, ok := chooseBootActuator().(grubActuator); !ok {
		t.Error("chooseBootActuator did not return a grubActuator, want one for a BIOS machine with an environment block")
	}
	if err := os.Remove(act.envPath()); err != nil {
		t.Fatalf("removing grubenv: %v", err)
	}
	if _, ok := chooseBootActuator().(noActuator); !ok {
		t.Error("chooseBootActuator did not return a noActuator, want one for a BIOS machine with no environment block")
	}
}

func TestChooseBootActuatorPrefersUEFI(t *testing.T) {
	fakeFirmware(t, map[string][]byte{})
	installedGRUBHome(t, map[string]string{"default_slot": "A"})
	if _, ok := chooseBootActuator().(efiActuator); !ok {
		t.Error("chooseBootActuator did not return an efiActuator, want one for a UEFI machine even with a boot home")
	}
}

func TestBIOSFirmwareFactsReadTheGRUBEnvironment(t *testing.T) {
	fakeBIOSFirmware(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10")
	installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": "B"})

	fw := biosFirmwareFacts()

	if fw.Mode != machine.FirmwareBIOS {
		t.Errorf("the mode is %v, want BIOS", fw.Mode)
	}
	if !strings.Contains(fw.BootCurrent, "liken slot A") {
		t.Errorf("BootCurrent is %q, want it to name slot A, the running slot", fw.BootCurrent)
	}
	if !strings.Contains(fw.BootNext, "liken slot B") {
		t.Errorf("BootNext is %q, want it to name slot B, the armed try_slot", fw.BootNext)
	}
	if len(fw.BootOrder) != 1 || !strings.Contains(fw.BootOrder[0], "liken slot A") {
		t.Errorf("BootOrder is %v, want it to name slot A, default_slot", fw.BootOrder)
	}
}

func TestBIOSFirmwareFactsWithoutAGRUBHome(t *testing.T) {
	// A QEMU machine booted with -kernel has BIOS mode and no boot
	// home.
	fakeBIOSFirmware(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1")
	fakeBootHomeMount(t)

	fw := biosFirmwareFacts()

	if fw.Mode != machine.FirmwareBIOS || fw.BootCurrent != "" || fw.BootNext != "" || len(fw.BootOrder) != 0 {
		t.Errorf("the firmware status is %+v, want BIOS mode with no other facts when grubenv is missing", fw)
	}
}

func TestGRUBActuatorCanArmWithAnEnvBlock(t *testing.T) {
	act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
	if err := act.canArmTrial("B"); err != nil {
		t.Errorf("canArmTrial returned %v, want nil with an installed environment block", err)
	}
}

// Without grubenv, GRUB has no try_slot to read, so arming must fail
// and not report a trial that will never run.
func TestGRUBActuatorArmTrialFailsWithoutAnEnvBlock(t *testing.T) {
	home := fakeBootHomeMount(t)
	act := grubActuator{grubDir: filepath.Join(home, "grub")}
	if _, err := act.armTrial("B"); err == nil {
		t.Error("armTrial returned nil with no environment block, want an error")
	}
	if _, err := os.Stat(act.envPath()); err == nil {
		t.Error("armTrial created grubenv, want none")
	}
}

// A grubenv that does not parse must not stop the boot sectors from
// being repaired.
func TestGRUBActuatorRepairsTheBootSectorsDespiteATornEnvBlock(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
	torn := bytes.Repeat([]byte{0xFF}, 1024)
	if err := os.WriteFile(act.envPath(), torn, 0o644); err != nil {
		t.Fatalf("writing the torn grubenv: %v", err)
	}

	act.assertProven("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk: %v", err)
	}
	if disk[0] != 0xB0 {
		t.Error("the first byte of the MBR is not 0xB0, want the boot sectors rewritten despite the torn environment block")
	}
	env, err := os.ReadFile(act.envPath())
	if err != nil {
		t.Fatalf("reading grubenv: %v", err)
	}
	if !bytes.Equal(env, torn) {
		t.Error("grubenv changed, want a block that does not parse left as it was")
	}
}

// In each case the repair cannot plan the boot sectors, so it must
// write neither them nor grub.cfg.
func TestGRUBActuatorRepairLeavesTheChainAloneWhenItCannotPlan(t *testing.T) {
	cases := []struct {
		name   string
		slot   string
		damage func(t *testing.T, sys, slotMount string)
	}{
		{"a slot letter that names no slot", "C", func(*testing.T, string, string) {}},
		{"a slot with no core image", "A", func(t *testing.T, _, slotMount string) {
			if err := os.Remove(filepath.Join(slotMount, "grub-core.img")); err != nil {
				t.Fatalf("removing grub-core.img: %v", err)
			}
		}},
		{"a core image for another platform", "A", func(t *testing.T, _, slotMount string) {
			if err := os.WriteFile(filepath.Join(slotMount, "grub-core.img"), make([]byte, 3*disks.SectorSize), 0o644); err != nil {
				t.Fatalf("writing grub-core.img: %v", err)
			}
		}},
		{"a disk with no biosBoot partition", "A", func(t *testing.T, sys, _ string) {
			if err := os.RemoveAll(filepath.Join(sys, "vdc", "vdc1")); err != nil {
				t.Fatalf("removing the biosBoot partition: %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sys, dev := installedBIOSDisk(t)
			slotMount, _, _ := grubArtifactsOnSlot(t)
			act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
			c.damage(t, sys, slotMount)
			before, err := os.ReadFile(filepath.Join(dev, "vdc"))
			if err != nil {
				t.Fatalf("reading the disk before the repair: %v", err)
			}

			act.repairBootChain(c.slot)

			after, err := os.ReadFile(filepath.Join(dev, "vdc"))
			if err != nil {
				t.Fatalf("reading the disk after the repair: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("the disk changed, want the boot sectors unchanged")
			}
			if _, err := os.Stat(filepath.Join(act.grubDir, "grub.cfg")); err == nil {
				t.Error("grub.cfg exists, want none when the repair stops early")
			}
		})
	}
}

// Without a machine name the repair fixes the boot sectors and does
// not write grub.cfg.
func TestGRUBActuatorRepairLeavesGrubCfgAloneWithoutAMachineName(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
	act.machineName = ""

	act.repairBootChain("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatalf("reading the disk: %v", err)
	}
	if disk[0] != 0xB0 {
		t.Error("the first byte of the MBR is not 0xB0, want the boot sectors rewritten with no machine name")
	}
	if _, err := os.Stat(filepath.Join(act.grubDir, "grub.cfg")); err == nil {
		t.Error("grub.cfg exists, want none when the machine name is empty")
	}
}

func TestSlotMountPathFollowsTheRoleMounts(t *testing.T) {
	slotA := fakeSlotAMount(t)
	slotB := fakeRoleMountPath(t, machine.SystemBRole)
	cases := []struct {
		slot string
		want string
	}{
		{"A", slotA},
		{"B", slotB},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.slot, func(t *testing.T) {
			if got := slotMountPath(c.slot); got != c.want {
				t.Errorf("slotMountPath(%q) = %q, want %q", c.slot, got, c.want)
			}
		})
	}
}
