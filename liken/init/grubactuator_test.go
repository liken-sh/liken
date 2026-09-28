package main

// Tests for the GRUB dialect of the boot actuator. These tests use a
// temporary directory as the boot home and the fake machine's disks.
// They cover arming, readback, the proven assertion, and the healing
// that re-derives the on-disk boot chain from a slot's artifacts.

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/disks"
	"github.com/liken-sh/liken/machine"
)

// installedGRUBHome builds a boot home the way the installer leaves
// it: a grub directory that holds an environment block with the
// given variables. It returns the actuator that points at this boot
// home.
func installedGRUBHome(t *testing.T, vars map[string]string) grubActuator {
	t.Helper()
	home := fakeBootHomeMount(t)
	grubDir := filepath.Join(home, "grub")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env, err := renderGRUBEnv(vars)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grubDir, "grubenv"), env, 0o644); err != nil {
		t.Fatal(err)
	}
	return grubActuator{grubDir: grubDir, machineName: "node-1"}
}

func TestGRUBActuatorArmsTheTrial(t *testing.T) {
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	armed, err := act.armTrial("B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(armed, "try_slot=B") {
		t.Errorf("the description names what was armed: %q", armed)
	}
	env, err := readGRUBEnv(act.envPath())
	if err != nil {
		t.Fatal(err)
	}
	if env["try_slot"] != "B" {
		t.Errorf("try_slot is the one-shot: %v", env)
	}
	if env["default_slot"] != "A" {
		t.Errorf("arming a trial must not move the standing preference: %v", env)
	}
}

func TestGRUBActuatorRefusesToArmWithoutAnEnvBlock(t *testing.T) {
	home := fakeBootHomeMount(t)
	act := grubActuator{grubDir: filepath.Join(home, "grub")}
	if err := act.canArmTrial("B"); err == nil {
		t.Error("no environment block means no safe way to arm a one-shot")
	}
}

func TestGRUBActuatorReadsTheFallbackBack(t *testing.T) {
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})
	if !act.fallbackLeads("A") {
		t.Error("default_slot=A is the standing preference")
	}
	if act.fallbackLeads("B") {
		t.Error("slot B does not lead; a verified fallback must not claim it does")
	}
}

func TestGRUBActuatorAssertProvenFlipsTheDefaultAndClearsTheTrial(t *testing.T) {
	// The disk fixture keeps the healing half of assertProven silent:
	// the slot carries no artifacts, so only the environment block
	// changes.
	installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": "B"})

	act.assertProven("B")

	env, err := readGRUBEnv(act.envPath())
	if err != nil {
		t.Fatal(err)
	}
	if env["default_slot"] != "B" {
		t.Errorf("promotion moves the standing preference: %v", env)
	}
	if env["try_slot"] != "" {
		t.Errorf("a stale one-shot must not survive the assertion: %v", env)
	}
}

func TestGRUBActuatorAssertProvenIsIdempotent(t *testing.T) {
	installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")
	before, err := os.ReadFile(act.envPath())
	if err != nil {
		t.Fatal(err)
	}
	act.assertProven("A")
	after, err := os.ReadFile(act.envPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("an assertion that changes nothing must write nothing")
	}
}

// grubArtifactsOnSlot puts the GRUB pair on a slot mount the way a
// verified release lays them down. It returns the mount and the two
// images (in the same shapes that fakePayload uses).
func grubArtifactsOnSlot(t *testing.T) (mount string, bootImg, coreImg []byte) {
	t.Helper()
	mount = fakeSlotAMount(t)
	bootImg = bytes.Repeat([]byte{0xB0}, disks.SectorSize)
	coreImg = bytes.Repeat([]byte{0xC0}, 3*disks.SectorSize)
	binary.LittleEndian.PutUint16(coreImg[grubBlocklistSegment:], grubLoadSegment)
	if err := os.WriteFile(filepath.Join(mount, "grub-boot.img"), bootImg, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "grub-core.img"), coreImg, 0o644); err != nil {
		t.Fatal(err)
	}
	return mount, bootImg, coreImg
}

func TestGRUBActuatorHealsTheBootSectors(t *testing.T) {
	// The installed BIOS disk's boot sectors start as zeroes. This is
	// exactly what a host leaves behind when it rewrites the MBR under
	// a running machine. Asserting the proven slot must put the whole
	// chain back from the slot's own artifacts.
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatal(err)
	}
	if disk[0] != 0xB0 {
		t.Error("the MBR boot code comes from grub-boot.img")
	}
	if got := binary.LittleEndian.Uint64(disk[grubKernelSectorOffset:]); got != 2048 {
		t.Errorf("the patched MBR points at the biosBoot partition: %d", got)
	}
	if disk[446+4] != 0xEE || disk[510] != 0x55 || disk[511] != 0xAA {
		t.Error("healing the boot code must not disturb the protective MBR")
	}
	core := disk[2048*disks.SectorSize:]
	if core[0] != 0xC0 {
		t.Error("the core image comes from grub-core.img")
	}
	if got := binary.LittleEndian.Uint64(core[grubBlocklistStart:]); got != 2049 {
		t.Errorf("the core's blocklist points at its own continuation: %d", got)
	}

	cfg, err := os.ReadFile(filepath.Join(act.grubDir, "grub.cfg"))
	if err != nil {
		t.Fatal("healing renders grub.cfg too:", err)
	}
	if !strings.Contains(string(cfg), "liken.machine=node-1") {
		t.Error("the healed config carries this machine's identity")
	}
}

func TestGRUBActuatorHealingIsQuietWhenTheChainAgrees(t *testing.T) {
	_, dev := installedBIOSDisk(t)
	grubArtifactsOnSlot(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")
	before, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatal(err)
	}
	act.assertProven("A")
	after, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a healthy chain must not be rewritten")
	}
}

func TestGRUBActuatorHealingSkipsASlotWithoutTheArtifacts(t *testing.T) {
	// A proven release from before liken carried GRUB artifacts says
	// nothing about the boot sectors. The chain that booted the
	// machine must stay untouched.
	_, dev := installedBIOSDisk(t)
	fakeSlotAMount(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": ""})

	act.assertProven("A")

	disk, err := os.ReadFile(filepath.Join(dev, "vdc"))
	if err != nil {
		t.Fatal(err)
	}
	if disk[0] != 0 {
		t.Error("no artifacts, no opinion: the boot code must stay as it was")
	}
}

func TestChooseBootActuatorSpeaksEachDialect(t *testing.T) {
	fakeBIOSRegime(t)
	act := installedGRUBHome(t, map[string]string{"default_slot": "A"})
	if _, ok := chooseBootActuator().(grubActuator); !ok {
		t.Error("a BIOS machine with an installed environment block speaks GRUB")
	}
	if err := os.Remove(act.envPath()); err != nil {
		t.Fatal(err)
	}
	if _, ok := chooseBootActuator().(noActuator); !ok {
		t.Error("a BIOS machine without an environment block has no dialect")
	}
}

func TestChooseBootActuatorPrefersUEFI(t *testing.T) {
	fakeFirmware(t, map[string][]byte{})
	installedGRUBHome(t, map[string]string{"default_slot": "A"})
	if _, ok := chooseBootActuator().(efiActuator); !ok {
		t.Error("a UEFI machine speaks to its firmware even when a boot home is present")
	}
}

func TestBIOSFirmwareFactsReadTheGRUBEnvironment(t *testing.T) {
	fakeBIOSRegime(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1 liken.slot=A panic=10")
	installedGRUBHome(t, map[string]string{"default_slot": "A", "try_slot": "B"})

	fw := biosFirmwareFacts()

	if fw.Mode != machine.FirmwareBIOS {
		t.Errorf("the mode is BIOS: %v", fw.Mode)
	}
	if !strings.Contains(fw.BootCurrent, "liken slot A") {
		t.Errorf("BootCurrent names the running slot: %q", fw.BootCurrent)
	}
	if !strings.Contains(fw.BootNext, "liken slot B") {
		t.Errorf("an armed try_slot reports as the one-shot: %q", fw.BootNext)
	}
	if len(fw.BootOrder) != 1 || !strings.Contains(fw.BootOrder[0], "liken slot A") {
		t.Errorf("default_slot is the standing preference: %v", fw.BootOrder)
	}
}

func TestBIOSFirmwareFactsWithoutAGRUBHome(t *testing.T) {
	// A -kernel lab boot: BIOS mode, no boot home, and no facts
	// beyond the mode itself.
	fakeBIOSRegime(t)
	fakeCmdline(t, "console=ttyS0 rdinit=/liken liken.machine=node-1")
	fakeBootHomeMount(t)

	fw := biosFirmwareFacts()

	if fw.Mode != machine.FirmwareBIOS || fw.BootCurrent != "" || fw.BootNext != "" || len(fw.BootOrder) != 0 {
		t.Errorf("no environment block, no facts to report: %+v", fw)
	}
}
