package main

// Tests for the hardware report's proposal composition. The
// composition is a pure function, so these tests drive the whole
// document from fabricated hardware, with no sysfs, no netlink, and no
// disk.

import (
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// sampleReport is a two-disk BIOS machine with one Realtek NIC that
// wants its PHY library loaded first. It exercises every branch that
// matters: a softdep-expanded chain, a durable disk distinct from the
// system disk, and the BIOS boot roles.
func sampleReport() hardwareReport {
	return hardwareReport{
		UEFI: false,
		Recommendations: []moduleRecommendation{{
			Device: "pci network device Realtek RTL8168 (modalias pci:v000010ECd00008168)",
			Chain:  []string{"realtek", "r8169"},
		}},
		Disks: []reportDisk{
			{Path: "/dev/sda", SizeBytes: 20 << 30, Model: "QEMU HARDDISK", Transport: "sata"},
			{Path: "/dev/sdb", SizeBytes: 50 << 30, Model: "QEMU HARDDISK", Transport: "sata"},
		},
		Interfaces: []reportInterface{
			{Name: "eth0", MAC: "52:54:00:12:34:56", Link: "up"},
		},
	}
}

// parseProposal parses the proposal back into a Machine and fails the
// test if it does not parse. Every proposal must be a real manifest.
func parseProposal(t *testing.T, r hardwareReport) *machine.Machine {
	t.Helper()
	text := composeHardwareReport(r)
	m, err := machine.Parse([]byte(text))
	if err != nil {
		t.Fatalf("the proposal must be a valid Machine manifest: %v\n%s", err, text)
	}
	return m
}

func TestProposalIsAValidInstallableManifest(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if m.APIVersion != "liken.sh/v1alpha1" {
		t.Errorf("apiVersion: %q", m.APIVersion)
	}
	if m.Kind != "Machine" {
		t.Errorf("kind: %q", m.Kind)
	}
	// The storage layout must pass the spec's own validation, so a
	// person can install from it after only renaming and sizing.
	if err := m.Spec.Storage.Validate(); err != nil {
		t.Errorf("the proposed storage must validate: %v", err)
	}
}

func TestProposalModulesAreTheSoftdepExpandedUnion(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if !slices.Equal(m.Spec.Modules, []string{"realtek", "r8169"}) {
		t.Errorf("spec.modules must be the full ordered chain: %v", m.Spec.Modules)
	}
}

func TestProposalDeduplicatesSharedSoftdeps(t *testing.T) {
	// Two devices whose chains share a name must not declare that name
	// twice: the loader would only load the same file again.
	r := sampleReport()
	r.Recommendations = []moduleRecommendation{
		{Device: "nic one", Chain: []string{"realtek", "r8169"}},
		{Device: "nic two", Chain: []string{"realtek", "r8125"}},
	}
	m := parseProposal(t, r)
	if !slices.Equal(m.Spec.Modules, []string{"realtek", "r8169", "r8125"}) {
		t.Errorf("shared softdeps must appear once, in order: %v", m.Spec.Modules)
	}
}

func TestProposalCarriesTheModuleEvidence(t *testing.T) {
	text := composeHardwareReport(sampleReport())
	// The device that named the drivers, and the ordered chain, must
	// both appear as a comment beside the module list.
	if !strings.Contains(text, "Realtek RTL8168") {
		t.Errorf("the module evidence must name the device:\n%s", text)
	}
	if !strings.Contains(text, "realtek, then r8169") {
		t.Errorf("the module evidence must show the load order:\n%s", text)
	}
}

func TestProposalCarriesTheDiskEvidence(t *testing.T) {
	text := composeHardwareReport(sampleReport())
	for _, want := range []string{
		"/dev/sda", "20.0 GiB", "QEMU HARDDISK", "(sata)",
		"/dev/sdb", "50.0 GiB",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the disk evidence is missing %q:\n%s", want, text)
		}
	}
}

func TestProposalNamesTheExcludedStick(t *testing.T) {
	r := sampleReport()
	r.StickPath = "/dev/sdc"
	text := composeHardwareReport(r)
	if !strings.Contains(text, "/dev/sdc is the installation stick") {
		t.Errorf("the proposal must account for the excluded stick:\n%s", text)
	}
	if strings.Contains(text, "device: /dev/sdc") {
		t.Errorf("no role may land on the stick:\n%s", text)
	}
}

func TestProposalPlacesDurableRolesOnTheSecondDisk(t *testing.T) {
	m := parseProposal(t, sampleReport())
	// The system slots land on the first disk, and the durable roles on
	// the second, so cluster state outlives a reinstall of the system
	// disk.
	if m.Spec.Storage.SystemA.Device != "/dev/sda" {
		t.Errorf("systemA device: %q", m.Spec.Storage.SystemA.Device)
	}
	if m.Spec.Storage.ClusterState.Device != "/dev/sdb" {
		t.Errorf("clusterState device: %q", m.Spec.Storage.ClusterState.Device)
	}
	if m.Spec.Storage.PodEphemeral.Size != "" {
		t.Errorf("the remainder role must omit its size: %q", m.Spec.Storage.PodEphemeral.Size)
	}
}

func TestProposalDeclaresBIOSRolesOnlyForBIOS(t *testing.T) {
	bios := parseProposal(t, sampleReport())
	if bios.Spec.Storage.BIOSBoot == nil || bios.Spec.Storage.BootHome == nil {
		t.Error("a BIOS machine's proposal must declare biosBoot and bootHome")
	}

	uefiReport := sampleReport()
	uefiReport.UEFI = true
	uefi := parseProposal(t, uefiReport)
	if uefi.Spec.Storage.BIOSBoot != nil || uefi.Spec.Storage.BootHome != nil {
		t.Error("a UEFI machine's proposal must not declare the GRUB roles")
	}
}

func TestProposalNamesTheObservedInterface(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if len(m.Spec.Network.Interfaces) != 1 || m.Spec.Network.Interfaces[0].Name != "eth0" {
		t.Errorf("the proposal must name the observed interface: %v", m.Spec.Network.Interfaces)
	}
	text := composeHardwareReport(sampleReport())
	if !strings.Contains(text, "52:54:00:12:34:56") || !strings.Contains(text, "link up") {
		t.Errorf("the interface evidence must carry the MAC and link:\n%s", text)
	}
}

func TestProposalHandlesEmptyHardware(t *testing.T) {
	// A report that found nothing (the lab's -kernel path, before any
	// module loads) must still produce a manifest that parses.
	m := parseProposal(t, hardwareReport{UEFI: true})
	if len(m.Spec.Modules) != 0 {
		t.Errorf("no recommendations means no modules: %v", m.Spec.Modules)
	}
	text := composeHardwareReport(hardwareReport{UEFI: true})
	if !strings.Contains(text, "modules: []") {
		t.Errorf("an empty module list must render explicitly:\n%s", text)
	}
}

func TestProposalPutsEveryRoleOnOneDiskWhenThereIsOnlyOne(t *testing.T) {
	r := sampleReport()
	r.Disks = r.Disks[:1]
	m := parseProposal(t, r)
	if m.Spec.Storage.ClusterState.Device != "/dev/sda" {
		t.Errorf("with one disk, every role lands on it: %q", m.Spec.Storage.ClusterState.Device)
	}
	if err := m.Spec.Storage.Validate(); err != nil {
		t.Errorf("a single-disk layout must still validate: %v", err)
	}
}
