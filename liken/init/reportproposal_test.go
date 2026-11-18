package main

// These tests build the proposal from made-up hardware. Besides
// checking that it parses, they check that its storage roles fit the
// disks they name, using mustFit (reportlayout_test.go).

import (
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// sampleReport is a BIOS machine with two disks and one Realtek NIC
// whose driver chain loads a PHY library first. It covers a chain with
// a softdep, a second disk for the durable roles, and the BIOS boot
// roles.
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

// parseProposal parses the proposal into a Machine and fails the test
// when it does not parse.
func parseProposal(t *testing.T, r hardwareReport) *machine.Machine {
	t.Helper()
	text := composeHardwareReport(r)
	m, err := machine.Parse([]byte(text))
	if err != nil {
		t.Fatalf("machine.Parse refused the proposal: %v, want a valid Machine manifest:\n%s", err, text)
	}
	return m
}

// mustInstall checks that the proposal parses, that its storage passes
// the spec's validation, and that every role fits the disk it names.
func mustInstall(t *testing.T, r hardwareReport) *machine.Machine {
	t.Helper()
	m := parseProposal(t, r)
	if err := m.Spec.Storage.Validate(); err != nil {
		t.Fatalf("the proposed storage does not validate: %v, want a valid storage spec", err)
	}
	mustFit(t, planStorageLayout(r.Disks, r.UEFI), r.Disks)
	return m
}

func TestProposalParsesAndItsRolesFitTheDisks(t *testing.T) {
	m := mustInstall(t, sampleReport())
	if m.APIVersion != "liken.sh/v1alpha1" {
		t.Errorf("apiVersion is %q, want liken.sh/v1alpha1", m.APIVersion)
	}
	if m.Kind != "Machine" {
		t.Errorf("kind is %q, want Machine", m.Kind)
	}
}

func TestProposalModulesAreTheSoftdepExpandedUnion(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if !slices.Equal(m.Spec.Modules, []string{"realtek", "r8169"}) {
		t.Errorf("spec.modules is %v, want the whole chain in load order, [realtek r8169]", m.Spec.Modules)
	}
}

func TestProposalDeduplicatesSharedSoftdeps(t *testing.T) {
	r := sampleReport()
	r.Recommendations = []moduleRecommendation{
		{Device: "nic one", Class: "network", Chain: []string{"realtek", "r8169"}},
		{Device: "nic two", Class: "network", Chain: []string{"realtek", "r8125"}},
	}
	m := parseProposal(t, r)
	if !slices.Equal(m.Spec.Modules, []string{"realtek", "r8169", "r8125"}) {
		t.Errorf("spec.modules is %v, want [realtek r8169 r8125], with each shared soft dependency once", m.Spec.Modules)
	}
}

func TestProposalCommentsNameTheDeviceAndLoadOrder(t *testing.T) {
	text := composeHardwareReport(sampleReport())
	if !strings.Contains(text, "Realtek RTL8168") {
		t.Errorf("the module evidence does not name the device Realtek RTL8168, want it:\n%s", text)
	}
	if !strings.Contains(text, "realtek, then r8169") {
		t.Errorf("the module evidence does not show the load order \"realtek, then r8169\", want it:\n%s", text)
	}
}

// storageDriverReport is sampleReport plus a storage controller whose
// driver the report loaded. The first disk appeared only after that
// driver loaded. The proposal cannot declare the driver, so it has to
// warn.
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
		t.Errorf("spec.modules is %v, want no megaraid_sas, because a storage driver in spec.modules loads too late", m.Spec.Modules)
	}
	if !slices.Contains(m.Spec.Modules, "r8169") {
		t.Errorf("spec.modules is %v, want it to keep the network driver r8169", m.Spec.Modules)
	}
}

func TestProposalWarnsAboutDisksBehindADriver(t *testing.T) {
	text := composeHardwareReport(storageDriverReport())
	for _, want := range []string{"megaraid_sas", "boot-modules.conf", "/dev/sda"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not contain %q, want it to explain the disk that the boot cannot reach:\n%s", want, text)
		}
	}
}

func TestWarningsReachTheConsole(t *testing.T) {
	lines := strings.Join(reportWarnings(storageDriverReport()), "\n")
	if !strings.Contains(lines, "megaraid_sas") || !strings.Contains(lines, "boot-modules.conf") {
		t.Errorf("the console lines have no fix for the unreachable disk, want one:\n%s", lines)
	}
	if len(reportWarnings(sampleReport())) != 0 {
		t.Errorf("reportWarnings returned %v, want none for a machine with nothing wrong", reportWarnings(sampleReport()))
	}
}

func TestProposalPrefersTheDiskAnInstallCanReach(t *testing.T) {
	m := mustInstall(t, storageDriverReport())
	if m.Spec.Storage.SystemA.Device != "/dev/sdb" {
		t.Errorf("systemA's device is %q, want /dev/sdb, the disk that the boot can reach", m.Spec.Storage.SystemA.Device)
	}
}

func TestProposalCommentsListEachDisk(t *testing.T) {
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

func TestProposalDiskEvidenceListsTheStableNameBesideTheModel(t *testing.T) {
	r := sampleReport()
	r.Disks[0].StableName = "/dev/disk/by-id/wwn-0x5002538d40a45c88"
	text := composeHardwareReport(r)
	if !strings.Contains(text, "QEMU HARDDISK  (sata)  /dev/disk/by-id/wwn-0x5002538d40a45c88") {
		t.Errorf("the report does not show the disk's stable name beside its model, want both on one line:\n%s", text)
	}
}

func TestProposalWritesTheStableNameAsTheDevice(t *testing.T) {
	r := sampleReport()
	r.Disks[0].StableName = "/dev/disk/by-id/wwn-0x5002538d40a45c88"
	m := parseProposal(t, r)
	if m.Spec.Storage.SystemA.Device != "/dev/disk/by-id/wwn-0x5002538d40a45c88" {
		t.Errorf("systemA's device is %q, want the disk's stable name /dev/disk/by-id/wwn-0x5002538d40a45c88", m.Spec.Storage.SystemA.Device)
	}
}

func TestProposalWritesTheKernelPathWhenNoStableNameExists(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if m.Spec.Storage.SystemA.Device != "/dev/sda" {
		t.Errorf("systemA's device is %q, want the kernel path /dev/sda for a disk with no stable name", m.Spec.Storage.SystemA.Device)
	}
}

func TestProposalNamesTheExcludedStick(t *testing.T) {
	r := sampleReport()
	r.StickPath = "/dev/sdc"
	text := composeHardwareReport(r)
	if !strings.Contains(text, "/dev/sdc is the install stick") {
		t.Errorf("the report does not say \"/dev/sdc is the install stick\", want it to explain the excluded disk:\n%s", text)
	}
	if strings.Contains(text, "device: /dev/sdc") {
		t.Errorf("the proposal places a role on the stick /dev/sdc, want none:\n%s", text)
	}
}

func TestProposalPlacesNoRoleOnADiskThatMightBeTheStick(t *testing.T) {
	r := sampleReport()
	r.Disks[1].MaybeStick = true
	text := composeHardwareReport(r)
	if strings.Contains(text, "device: /dev/sdb") {
		t.Errorf("a disk that might be the stick has a role, want none:\n%s", text)
	}
	if !strings.Contains(text, "cannot tell which disk is the stick") {
		t.Errorf("the report does not say why the disk is unused, want \"cannot tell which disk is the stick\":\n%s", text)
	}
	mustInstall(t, r)
}

func TestProposalPlacesDurableRolesOnTheSecondDisk(t *testing.T) {
	m := mustInstall(t, sampleReport())
	// The system roles go on the first disk and the durable roles on
	// the second, so cluster state survives a reinstall of the system
	// disk.
	if m.Spec.Storage.SystemA.Device != "/dev/sda" {
		t.Errorf("the systemA device is %q, want /dev/sda", m.Spec.Storage.SystemA.Device)
	}
	if m.Spec.Storage.ClusterState.Device != "/dev/sdb" {
		t.Errorf("the clusterState device is %q, want /dev/sdb", m.Spec.Storage.ClusterState.Device)
	}
	if m.Spec.Storage.PodEphemeral.Size != "" {
		t.Errorf("podEphemeral's size is %q, want none for the role that takes the rest of the disk", m.Spec.Storage.PodEphemeral.Size)
	}
}

func TestProposalDeclaresBIOSRolesOnlyForBIOS(t *testing.T) {
	bios := parseProposal(t, sampleReport())
	if bios.Spec.Storage.BIOSBoot == nil || bios.Spec.Storage.BootHome == nil {
		t.Errorf("biosBoot is %+v and bootHome is %+v, want both declared on a BIOS machine", bios.Spec.Storage.BIOSBoot, bios.Spec.Storage.BootHome)
	}

	uefiReport := sampleReport()
	uefiReport.UEFI = true
	uefi := parseProposal(t, uefiReport)
	if uefi.Spec.Storage.BIOSBoot != nil || uefi.Spec.Storage.BootHome != nil {
		t.Errorf("biosBoot is %+v and bootHome is %+v, want neither on a UEFI machine", uefi.Spec.Storage.BIOSBoot, uefi.Spec.Storage.BootHome)
	}
}

func TestProposalNamesTheConnectedInterface(t *testing.T) {
	m := parseProposal(t, sampleReport())
	if len(m.Spec.Network.Interfaces) != 1 || m.Spec.Network.Interfaces[0].Name != "eth0" {
		t.Errorf("the interfaces are %v, want only the observed eth0", m.Spec.Network.Interfaces)
	}
	text := composeHardwareReport(sampleReport())
	if !strings.Contains(text, "52:54:00:12:34:56") || !strings.Contains(text, "link up") {
		t.Errorf("the interface evidence has no MAC address or link state, want both:\n%s", text)
	}
}

func TestProposalLeavesUnconnectedPortsUndeclared(t *testing.T) {
	// Two ports have no cable. Declaring them would add a DHCP wait of
	// up to 30 seconds each to every boot.
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
	// eth3's driver does not track the carrier, so its link state is
	// "unknown" and the report declares it.
	if !slices.Equal(names, []string{"eth0", "eth3"}) {
		t.Errorf("the declared ports are %v, want eth0 and eth3", names)
	}

	text := composeHardwareReport(r)
	for _, want := range []string{"#- name: eth1", "#- name: eth2", "52:54:00:00:00:02"} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal has no %q, want each port with no carrier listed as evidence:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "thirty seconds") {
		t.Errorf("the proposal does not say how a declared port with no carrier delays the boot, want it to:\n%s", text)
	}
}

func TestProposalWithNoCarrierAnywhereDeclaresNoInterface(t *testing.T) {
	r := sampleReport()
	r.Interfaces = []reportInterface{{Name: "eth0", MAC: "52:54:00:00:00:01", Link: "down"}}
	m := parseProposal(t, r)
	if len(m.Spec.Network.Interfaces) != 0 {
		t.Errorf("the interfaces are %v, want none when no port has a carrier", m.Spec.Network.Interfaces)
	}
	if !strings.Contains(composeHardwareReport(r), "- name: eth0") {
		t.Errorf("the report does not list \"- name: eth0\", want the port named so a person can declare it:\n%s", composeHardwareReport(r))
	}
}

func TestProposalHandlesEmptyHardware(t *testing.T) {
	m := parseProposal(t, hardwareReport{UEFI: true})
	if len(m.Spec.Modules) != 0 {
		t.Errorf("spec.modules is %v, want none with no recommendations", m.Spec.Modules)
	}
	text := composeHardwareReport(hardwareReport{UEFI: true})
	if !strings.Contains(text, "modules: []") {
		t.Errorf("the report has no \"modules: []\", want the empty list written out:\n%s", text)
	}
	if !strings.Contains(text, "storage: {}") {
		t.Errorf("the report has no \"storage: {}\", want it with no disk:\n%s", text)
	}
}

func TestProposalPutsEveryRoleOnOneDiskWhenThereIsOnlyOne(t *testing.T) {
	r := sampleReport()
	r.Disks = r.Disks[:1]
	m := mustInstall(t, r)
	if m.Spec.Storage.ClusterState.Device != "/dev/sda" {
		t.Errorf("clusterState is on %q, want /dev/sda, the only disk", m.Spec.Storage.ClusterState.Device)
	}
}

func TestProposalSizesTheRolesToASmallDisk(t *testing.T) {
	// Only the 4 GiB disk can hold the system roles, which leaves the
	// 2 GiB disk for clusterState. That disk is too small for
	// containerd's image store, so the proposal declares no
	// clusterState. A smaller clusterState would install, then fail
	// when an image pull filled it.
	r := sampleReport()
	r.UEFI = true
	r.Disks = []reportDisk{
		{Name: "sda", Path: "/dev/sda", SizeBytes: 2 << 30, Transport: "sata"},
		{Name: "sdb", Path: "/dev/sdb", SizeBytes: 4 << 30, Transport: "sata"},
	}
	m := mustInstall(t, r)

	if m.Spec.Storage.SystemA.Device != "/dev/sdb" {
		t.Errorf("systemA is on %q, want /dev/sdb, the only disk that can hold the system roles", m.Spec.Storage.SystemA.Device)
	}
	if m.Spec.Storage.ClusterState != nil {
		t.Errorf("clusterState is %+v, want none because no disk can hold an image store", m.Spec.Storage.ClusterState)
	}
	if text := composeHardwareReport(r); !strings.Contains(text, "image store") {
		t.Errorf("the report does not mention the image store, want it to say why clusterState is missing:\n%s", text)
	}
}

func TestProposalExplainsWhatEachDurableRoleHolds(t *testing.T) {
	// clusterState's size depends on the images the node runs.
	// podStorage holds the volumes, and has the "size to" marker that
	// the header tells the person to edit.
	text := composeHardwareReport(sampleReport())
	for _, want := range []string{
		"size: 6Gi",
		"size: 4Gi  # size to your workloads' volumes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the proposal has no %q, want it:\n%s", want, text)
		}
	}
	// The notes are wrapped across comment lines, so check them with
	// the wrapping removed.
	prose := unwrapped(text)
	for _, want := range []string{
		"containerd's image store",
		"local-path provisioner",
		"clusterState cannot grow later",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("the report does not contain %q, want the note that explains clusterState:\n%s", want, text)
		}
	}
}

// unwrapped joins the words of the proposal's comment lines into one
// string, so that a test does not depend on where writeComment broke
// a line.
func unwrapped(text string) string {
	var words []string
	for line := range strings.Lines(text) {
		words = append(words, strings.Fields(strings.TrimLeft(strings.TrimSpace(line), "# "))...)
	}
	return strings.Join(words, " ")
}

// withGPU is sampleReport plus an integrated GPU with no driver
// loaded.
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
		t.Errorf("the report has no \"#- i915\", want the driver that would make the GPU claimable:\n%s", text)
	}
	if !strings.Contains(unwrapped(text), "UHD Graphics") {
		t.Errorf("the report does not name UHD Graphics, want the device that the driver is for:\n%s", text)
	}
}

func TestProposalLeavesClaimableHardwareUndeclared(t *testing.T) {
	m := mustInstall(t, withGPU())
	if slices.Contains(m.Spec.Modules, "i915") {
		t.Errorf("spec.modules is %v, want no i915, because a claimable device's driver is commented out", m.Spec.Modules)
	}
}

func TestProposalWithNoClaimableHardwareSaysNothing(t *testing.T) {
	text := composeHardwareReport(sampleReport())
	if strings.Contains(text, "hardware that nothing drives") {
		t.Errorf("the report has a \"hardware that nothing drives\" section, want none for a machine with nothing to claim:\n%s", text)
	}
}

func TestProposalRefusesToLayOutADiskThatIsTooSmall(t *testing.T) {
	r := sampleReport()
	r.Disks = []reportDisk{{Name: "sda", Path: "/dev/sda", SizeBytes: 2 << 30, Transport: "sata"}}
	m := parseProposal(t, r)
	if m.Spec.Storage.SystemA != nil {
		t.Errorf("the storage is %+v, want no systemA when no layout fits", m.Spec.Storage)
	}
	text := composeHardwareReport(r)
	if !strings.Contains(text, "No disk here can hold") {
		t.Errorf("the report does not say \"No disk here can hold\", want it to say the disk is too small:\n%s", text)
	}
}
