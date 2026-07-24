package main

// Tests for the hardware report's proposal composition. The
// composition is a pure function, so these tests drive the whole
// document from fabricated hardware, with no sysfs, no netlink, and no
// disk.
//
// The proposal's promise is that a person can install from it after
// renaming the machine, so the checks here go past "it parses". A
// proposal must also survive the arithmetic the install runs over it,
// which is what mustFit (reportlayout_test.go) applies.

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
			Class:  "network",
			Chain:  []string{"realtek", "r8169"},
		}},
		Disks: []reportDisk{
			{Name: "sda", Path: "/dev/sda", SizeBytes: 20 << 30, Model: "QEMU HARDDISK", Transport: "sata"},
			{Name: "sdb", Path: "/dev/sdb", SizeBytes: 50 << 30, Model: "QEMU HARDDISK", Transport: "sata"},
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

// mustInstall is the whole promise in one call: the proposal parses,
// its roles satisfy the spec's own rules, and every role fits the disk
// it names when the installer lays the partitions down.
func mustInstall(t *testing.T, r hardwareReport) *machine.Machine {
	t.Helper()
	m := parseProposal(t, r)
	if err := m.Spec.Storage.Validate(); err != nil {
		t.Fatalf("the proposed storage must validate: %v", err)
	}
	mustFit(t, planStorageLayout(r.Disks, r.UEFI), r.Disks)
	return m
}

func TestProposalParsesAndItsRolesFitTheDisks(t *testing.T) {
	m := mustInstall(t, sampleReport())
	if m.APIVersion != "liken.sh/v1alpha1" {
		t.Errorf("apiVersion: %q", m.APIVersion)
	}
	if m.Kind != "Machine" {
		t.Errorf("kind: %q", m.Kind)
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
		{Device: "nic one", Class: "network", Chain: []string{"realtek", "r8169"}},
		{Device: "nic two", Class: "network", Chain: []string{"realtek", "r8125"}},
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

// storageDriverReport is a machine whose only disk hangs off an HBA
// whose driver the report loaded: the case where the proposal cannot
// keep its promise, and has to say so instead of hiding the reason in
// a module list that would never work.
func storageDriverReport() hardwareReport {
	r := sampleReport()
	r.Recommendations = append(r.Recommendations, moduleRecommendation{
		Device: "pci storage device Broadcom MegaRAID (modalias pci:v00001000d00000097)",
		Class:  "storage",
		Chain:  []string{"megaraid_sas"},
	})
	r.Disks[0].BehindModules = []string{"megaraid_sas"}
	return r
}

func TestProposalNeverDeclaresAStorageDriver(t *testing.T) {
	m := parseProposal(t, storageDriverReport())
	if slices.Contains(m.Spec.Modules, "megaraid_sas") {
		t.Errorf("a storage driver cannot load in time to matter: %v", m.Spec.Modules)
	}
	if !slices.Contains(m.Spec.Modules, "r8169") {
		t.Errorf("the network driver must still be declared: %v", m.Spec.Modules)
	}
}

func TestProposalSaysWhichDisksAnInstallCannotReach(t *testing.T) {
	text := composeHardwareReport(storageDriverReport())
	for _, want := range []string{"megaraid_sas", "boot-modules.conf", "/dev/sda"} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal must explain the unreachable disk (%q):\n%s", want, text)
		}
	}
}

func TestWarningsReachTheConsole(t *testing.T) {
	lines := strings.Join(reportWarnings(storageDriverReport()), "\n")
	if !strings.Contains(lines, "megaraid_sas") || !strings.Contains(lines, "boot-modules.conf") {
		t.Errorf("the console must carry the unreachable disk's fix:\n%s", lines)
	}
	if len(reportWarnings(sampleReport())) != 0 {
		t.Error("a machine with nothing wrong warns about nothing")
	}
}

func TestProposalPrefersTheDiskAnInstallCanReach(t *testing.T) {
	m := mustInstall(t, storageDriverReport())
	if m.Spec.Storage.SystemA.Device != "/dev/sdb" {
		t.Errorf("the system slots belong on the reachable disk: %q", m.Spec.Storage.SystemA.Device)
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

func TestProposalPlacesNoRoleOnADiskThatMightBeTheStick(t *testing.T) {
	r := sampleReport()
	r.Disks[1].MaybeStick = true
	text := composeHardwareReport(r)
	if strings.Contains(text, "device: /dev/sdb") {
		t.Errorf("a disk that might be the stick must carry no role:\n%s", text)
	}
	if !strings.Contains(text, "cannot tell which disk is the stick") {
		t.Errorf("the proposal must say why the disk is unused:\n%s", text)
	}
	mustInstall(t, r)
}

func TestProposalPlacesDurableRolesOnTheSecondDisk(t *testing.T) {
	m := mustInstall(t, sampleReport())
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

func TestProposalNamesTheConnectedInterface(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if len(m.Spec.Network.Interfaces) != 1 || m.Spec.Network.Interfaces[0].Name != "eth0" {
		t.Errorf("the proposal must name the observed interface: %v", m.Spec.Network.Interfaces)
	}
	text := composeHardwareReport(sampleReport())
	if !strings.Contains(text, "52:54:00:12:34:56") || !strings.Contains(text, "link up") {
		t.Errorf("the interface evidence must carry the MAC and link:\n%s", text)
	}
}

func TestProposalLeavesDarkPortsUndeclared(t *testing.T) {
	// Three of the four ports on this board have no cable. Declaring
	// them would add thirty seconds of DHCP wait each, to every boot.
	r := sampleReport()
	r.Interfaces = []reportInterface{
		{Name: "eth0", MAC: "52:54:00:00:00:01", Link: "up"},
		{Name: "eth1", MAC: "52:54:00:00:00:02", Link: "down"},
		{Name: "eth2", MAC: "52:54:00:00:00:03", Link: "down"},
		{Name: "eth3", MAC: "52:54:00:00:00:04", Link: "unknown"},
	}
	m := parseProposal(t, r)

	var names []string
	for _, ifc := range m.Spec.Network.Interfaces {
		names = append(names, ifc.Name)
	}
	// eth3's driver does not track the carrier, so the report cannot
	// call the port dark, and declares it.
	if !slices.Equal(names, []string{"eth0", "eth3"}) {
		t.Errorf("only the ports that could carry traffic may be declared: %v", names)
	}

	text := composeHardwareReport(r)
	for _, want := range []string{"#- name: eth1", "#- name: eth2", "52:54:00:00:00:02"} {
		if !strings.Contains(text, want) {
			t.Errorf("a dark port must stay as evidence (%q):\n%s", want, text)
		}
	}
	if !strings.Contains(text, "thirty seconds") {
		t.Errorf("the proposal must say what a declared dark port costs:\n%s", text)
	}
}

// twoRealtekPorts is the board that made this a problem: two ports of
// one model, on one driver, one wired and one not. The kernel decides
// which is eth0 by probe order, and nothing about that order says
// which port has the cable in it.
func twoRealtekPorts() hardwareReport {
	r := sampleReport()
	r.Interfaces = []reportInterface{
		{Name: "eth0", MAC: "e0:51:d8:aa:bb:01", Link: "up", Driver: "r8169"},
		{Name: "eth1", MAC: "e0:51:d8:aa:bb:02", Link: "down", Driver: "r8169"},
	}
	return r
}

func TestProposalDeclaresPortsByMACWhenTwoShareADriver(t *testing.T) {
	m := parseProposal(t, twoRealtekPorts())
	interfaces := m.Spec.Network.Interfaces
	if len(interfaces) != 1 {
		t.Fatalf("only the wired port is declared: %v", interfaces)
	}
	if interfaces[0].MAC != "e0:51:d8:aa:bb:01" {
		t.Errorf("the declaration must state the address: %+v", interfaces[0])
	}
	if interfaces[0].Name != "" {
		t.Errorf("a name here would be the guess this avoids: %+v", interfaces[0])
	}
}

func TestProposalOffersTheDarkPortByMACToo(t *testing.T) {
	// A person who moves the cable uncomments one line. That line has
	// to identify its port the same way, or it reintroduces the guess.
	text := composeHardwareReport(twoRealtekPorts())
	if !strings.Contains(text, "#- mac: e0:51:d8:aa:bb:02") {
		t.Errorf("the dark port must be offered by address:\n%s", text)
	}
}

func TestProposalKeepsTheKernelNamesAsEvidence(t *testing.T) {
	// The names are still what the machine's console prints, so a
	// person matching the report against a boot log needs them, and
	// the driver is the report's reason for choosing addresses.
	text := composeHardwareReport(twoRealtekPorts())
	for _, want := range []string{"eth0 on r8169", "eth1 on r8169", "more than one port on the same driver"} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal must carry %q:\n%s", want, text)
		}
	}
}

func TestProposalKeepsNamesWhenNoDriverHasTwoPorts(t *testing.T) {
	// One port per driver leaves the names unambiguous, and a name
	// is worth more there: the same manifest then describes every
	// machine built to the same recipe, and a replacement card
	// inherits the declaration.
	r := sampleReport()
	r.Interfaces = []reportInterface{
		{Name: "eth0", MAC: "e0:51:d8:aa:bb:01", Link: "up", Driver: "r8169"},
		{Name: "eth1", MAC: "e0:51:d8:aa:bb:02", Link: "up", Driver: "igb"},
	}
	m := parseProposal(t, r)
	for _, ifc := range m.Spec.Network.Interfaces {
		if ifc.Name == "" || ifc.MAC != "" {
			t.Errorf("unambiguous names stay names: %+v", ifc)
		}
	}
}

func TestProposalWithNoCarrierAnywhereOffersTheMACsOfSharedPorts(t *testing.T) {
	// A machine with two ports on one driver and no cable in either
	// still cannot be described by name, so the ports it offers are
	// offered by address.
	r := twoRealtekPorts()
	r.Interfaces[0].Link = "down"
	text := composeHardwareReport(r)
	if !strings.Contains(text, "#   - mac: e0:51:d8:aa:bb:01") {
		t.Errorf("a dark machine's ports must be offered by address:\n%s", text)
	}
}

func TestProposalWithNoCarrierAnywhereDeclaresNoInterface(t *testing.T) {
	r := sampleReport()
	r.Interfaces = []reportInterface{{Name: "eth0", MAC: "52:54:00:00:00:01", Link: "down"}}
	m := parseProposal(t, r)
	if len(m.Spec.Network.Interfaces) != 0 {
		t.Errorf("no carrier means no declaration: %v", m.Spec.Network.Interfaces)
	}
	if !strings.Contains(composeHardwareReport(r), "- name: eth0") {
		t.Error("the port a person could declare must still be named")
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
	if !strings.Contains(text, "storage: {}") {
		t.Errorf("no disk means no storage section:\n%s", text)
	}
}

func TestProposalPutsEveryRoleOnOneDiskWhenThereIsOnlyOne(t *testing.T) {
	r := sampleReport()
	r.Disks = r.Disks[:1]
	m := mustInstall(t, r)
	if m.Spec.Storage.ClusterState.Device != "/dev/sda" {
		t.Errorf("with one disk, every role lands on it: %q", m.Spec.Storage.ClusterState.Device)
	}
}

func TestProposalSizesTheRolesToASmallDisk(t *testing.T) {
	// The parity drill's own disks: 2 GiB and 4 GiB. Only the second
	// can hold liken's roles, so the 2 GiB disk carries the cluster's
	// data, and it is under the floor an image store needs. The
	// proposal names no clusterState there rather than a size that
	// installs and then fails on a pull.
	r := sampleReport()
	r.UEFI = true
	r.Disks = []reportDisk{
		{Name: "sda", Path: "/dev/sda", SizeBytes: 2 << 30, Transport: "sata"},
		{Name: "sdb", Path: "/dev/sdb", SizeBytes: 4 << 30, Transport: "sata"},
	}
	m := mustInstall(t, r)

	if m.Spec.Storage.SystemA.Device != "/dev/sdb" {
		t.Errorf("the system slots must land on the disk that can hold them: %q", m.Spec.Storage.SystemA.Device)
	}
	if m.Spec.Storage.ClusterState != nil {
		t.Errorf("no disk here can hold an image store: %+v", m.Spec.Storage.ClusterState)
	}
	if text := composeHardwareReport(r); !strings.Contains(text, "image store") {
		t.Errorf("the proposal must say why clusterState is missing:\n%s", text)
	}
}

func TestProposalTeachesWhatEachDurableRoleHolds(t *testing.T) {
	// A person reading the proposal has to know which size is theirs to
	// choose. clusterState follows the images the node runs; podStorage
	// is the volume pool, and it carries the "size to" marker the
	// header tells them to edit.
	text := composeHardwareReport(sampleReport())
	for _, want := range []string{
		"size: 6Gi",
		"size: 4Gi  # size to your workloads' volumes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal must carry %q:\n%s", want, text)
		}
	}
	// The notes are wrapped into comment lines, so a sentence a person
	// reads across two lines is one the test must read the same way.
	prose := unwrapped(text)
	for _, want := range []string{
		"containerd's image store",
		"local-path provisioner",
		"clusterState cannot grow later",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("the proposal must teach %q:\n%s", want, text)
		}
	}
}

// unwrapped turns the proposal's wrapped comment lines back into one
// run of words, so a test can assert on a sentence without pinning
// where the wrapper broke it.
func unwrapped(text string) string {
	var words []string
	for line := range strings.Lines(text) {
		words = append(words, strings.Fields(strings.TrimLeft(strings.TrimSpace(line), "# "))...)
	}
	return strings.Join(words, " ")
}

// withGPU is the testbed: a machine whose integrated GPU nothing
// drives, because an install needs no display.
func withGPU() hardwareReport {
	r := sampleReport()
	r.Claimable = []moduleRecommendation{{
		Device: "pci display device Intel Alder Lake-N [UHD Graphics] (modalias pci:v00008086d000046D1)",
		Class:  "display",
		Chain:  []string{"i915"},
	}}
	return r
}

func TestProposalNamesHardwareAWorkloadCouldClaim(t *testing.T) {
	text := composeHardwareReport(withGPU())
	if !strings.Contains(text, "#- i915") {
		t.Errorf("the proposal must name the driver that would make the GPU claimable:\n%s", text)
	}
	if !strings.Contains(unwrapped(text), "UHD Graphics") {
		t.Errorf("the proposal must say which device the driver is for:\n%s", text)
	}
}

func TestProposalLeavesClaimableHardwareUndeclared(t *testing.T) {
	// The line stays commented, so the proposal installs exactly as it
	// reads. Driving a GPU is a decision a person makes, and the report
	// changes nothing on the machine it describes.
	m := mustInstall(t, withGPU())
	if slices.Contains(m.Spec.Modules, "i915") {
		t.Errorf("claimable hardware must not reach spec.modules: %v", m.Spec.Modules)
	}
}

func TestProposalWithNoClaimableHardwareSaysNothing(t *testing.T) {
	text := composeHardwareReport(sampleReport())
	if strings.Contains(text, "hardware that nothing drives") {
		t.Errorf("a machine with nothing to claim gets no section:\n%s", text)
	}
}

func TestProposalRefusesToLayOutADiskThatIsTooSmall(t *testing.T) {
	r := sampleReport()
	r.Disks = []reportDisk{{Name: "sda", Path: "/dev/sda", SizeBytes: 2 << 30, Transport: "sata"}}
	m := parseProposal(t, r)
	if m.Spec.Storage.SystemA != nil {
		t.Errorf("a layout that cannot exist must not be written: %+v", m.Spec.Storage)
	}
	text := composeHardwareReport(r)
	if !strings.Contains(text, "No disk here can hold") {
		t.Errorf("the proposal must say the disk is too small:\n%s", text)
	}
}
