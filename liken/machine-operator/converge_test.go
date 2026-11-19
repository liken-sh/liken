package main

// The convergence decisions are pure functions of plain values, so
// these tests need no cluster, disk, or mount.

import (
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/machine"
)

func specWith(storage machine.StorageSpec) machine.MachineSpec {
	return machine.MachineSpec{Storage: storage, Network: labNetwork()}
}

// labNetwork is the lab machine's network: a DHCP uplink and a
// cluster segment with a static address that its peers use.
func labNetwork() machine.NetworkSpec {
	return machine.NetworkSpec{Interfaces: []machine.InterfaceSpec{
		{Name: "eth0"},
		{Name: "eth1", Address: "10.10.0.1/24"},
	}}
}

// labStorage is the lab machine's storage: five roles on two disks.
func labStorage() machine.StorageSpec {
	return machine.StorageSpec{
		MachineState:     &machine.StorageRole{Device: "/dev/vda", Size: "64Mi"},
		MachineEphemeral: &machine.StorageRole{Device: "/dev/vdb", Size: "512Mi"},
		ClusterState:     &machine.StorageRole{Device: "/dev/vda"},
		PodStorage:       &machine.StorageRole{Device: "/dev/vdb", Size: "2Gi"},
		PodEphemeral:     &machine.StorageRole{Device: "/dev/vdb"},
	}
}

// bootNetwork is labNetwork as the boot records it. A nil record
// means the boot did not record its network.
func bootNetwork() *machine.NetworkSpec {
	spec := labNetwork()
	return &spec
}

// labFacts is a healthy boot of the lab machine: every storage role
// has a partition and the boot record is complete.
func labFacts() *machine.MachineStatus {
	facts := &machine.MachineStatus{
		Hardware: machine.HardwareStatus{
			BlockDevices: []machine.BlockDevice{
				{Name: "vda", SizeBytes: 2 << 30},
				{Name: "vdb", SizeBytes: 4 << 30},
			},
		},
		Storage: machine.StorageStatus{
			MachineState:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda1", CapacityBytes: 64 << 20},
			MachineEphemeral: machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb1", CapacityBytes: 512 << 20},
			ClusterState:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vda2", CapacityBytes: 1 << 30},
			PodStorage:       machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb2", CapacityBytes: 2 << 30},
			PodEphemeral:     machine.StorageRoleStatus{Backing: machine.BackingPartition, Device: "vdb3", CapacityBytes: 1 << 30},
		},
		Boot: machine.BootStatus{
			ManifestSource: machine.ManifestSourceProven,
			ManifestHash:   "abc123",
			Storage:        labStorage(),
			Network:        bootNetwork(),
		},
	}
	return facts
}

func labMachine() *machine.Machine {
	return &machine.Machine{
		APIVersion: api.APIVersion,
		Kind:       "Machine",
		Metadata:   api.ObjectMeta{Name: "liken-dev"},
		Spec:       specWith(labStorage()),
	}
}

func TestDecideConvergenceLoadsAddedModulesLive(t *testing.T) {
	// The manifest is still staged, so the module loads again after a
	// reboot.
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "LoadRequested" {
		t.Fatalf("the condition is %+v, want reason LoadRequested", conv.condition)
	}
	if !conv.stage || !conv.requestLoad || conv.requestReboot || conv.requestRestart {
		t.Errorf("an additive modules edit stages and loads, nothing more: %+v", conv)
	}
}

func TestLiveLoadNeedsNoPolicyOrTurn(t *testing.T) {
	// A live load drains nothing and restarts nothing, so it needs
	// neither a turn nor an approval under rebootPolicy: Manual.
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.RebootPolicy = machine.RebootManual
	conv := decideConvergence(m, labFacts(), nil, "", turnAwaiting)
	if conv.condition.Reason != "LoadRequested" || !conv.requestLoad {
		t.Errorf("the condition is %+v, want a live load that waits for no policy and no turn", conv.condition)
	}
}

func TestModuleRetractionStillConvergesByReboot(t *testing.T) {
	// The kernel has no safe way to unload a driver that is in use,
	// so removing a module needs a reboot.
	m := labMachine()
	facts := labFacts()
	facts.Boot.Modules = []string{"nvidia"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("a retraction needs the reboot: %+v", conv.condition)
	}
}

// A process inherits its resource limits when it starts, so a live
// load cannot change the limits of a running k3s. It would still
// promote the manifest, and the machine would report limits it never
// applied.
func TestRlimitDriftConvergesByReboot(t *testing.T) {
	m := labMachine()
	m.Spec.Rlimits = map[string]string{"nofile": "524288"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("an rlimit edit needs the reboot: %+v", conv.condition)
	}
}

// The load would promote the whole manifest, rlimits included.
func TestAnAddedModuleBesideARlimitEditConvergesByReboot(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.Rlimits = map[string]string{"nofile": "524288"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("the condition is %+v, want a reboot because an rlimit changed beside the added module", conv.condition)
	}
}

// The live-load check counts drift lines and does not list fields, so
// a spec field added later needs a reboot by default. This test fails
// if the count becomes a list of field checks.
func TestAnUnrecognizedDiffNeverReachesTheLivePath(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	facts := labFacts()
	// The network drift stands for a future field.
	facts.Boot.Network = &machine.NetworkSpec{
		Interfaces: []machine.InterfaceSpec{{Name: "eth9"}},
	}
	m.Spec.Network = machine.NetworkSpec{
		Interfaces: []machine.InterfaceSpec{{Name: "eth0"}},
	}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.requestLoad {
		t.Errorf("a diff beside the added module reached the live path: %+v", conv.condition)
	}
}

func TestMixedModuleAndStorageDriftConvergesByReboot(t *testing.T) {
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.Storage.PodStorage.Size = "3Gi"
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("the condition is %+v, want a reboot because the storage changed", conv.condition)
	}
}

func TestDecideConvergenceStagesOnModulesDrift(t *testing.T) {
	m := labMachine()
	facts := labFacts()
	facts.Boot.Modules = []string{"zram"}
	m.Spec.Modules = []string{"nvidia"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("the condition is %+v, want reason RebootPending", conv.condition)
	}
	if !conv.stage {
		t.Errorf("a modules edit stages like any other machine change: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "nvidia") {
		t.Errorf("the message is %q, want it to contain the diff", conv.condition.Message)
	}
}

func TestDecideConvergenceStagesANetworkEdit(t *testing.T) {
	// A nameserver applies only at boot. Reporting Converged would
	// say the edit applied while the machine runs the old network.
	m := labMachine()
	m.Spec.Network.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("the condition is %+v, want reason RebootPending", conv.condition)
	}
	if !conv.stage || conv.requestReboot || conv.requestLoad {
		t.Errorf("Manual policy stages the network edit and waits: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "nameservers 10.10.0.1") {
		t.Errorf("the message is %q, want it to contain the diff", conv.condition.Message)
	}
}

func TestNetworkEditFollowsTheSameRebootPolicyAsStorage(t *testing.T) {
	m := labMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	m.Spec.Network.Interfaces[1].Address = "10.10.0.9/24"
	if conv := decideConvergence(m, labFacts(), nil, "", turnAwaiting); conv.condition.Reason != "AwaitingTurn" {
		t.Errorf("the condition is %+v, want AwaitingTurn for a cluster member with no turn", conv.condition)
	}
	conv := decideConvergence(m, labFacts(), nil, "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Errorf("the condition is %+v, want RebootRequested because the machine has a turn", conv.condition)
	}
}

func TestANetworkEditIsNeverLoadedInPlace(t *testing.T) {
	// Nothing changes an interface's network in the running kernel,
	// and init refuses a live load with network drift too.
	m := labMachine()
	m.Spec.Modules = []string{"nvidia"}
	m.Spec.Network.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || conv.requestLoad {
		t.Errorf("the condition is %+v, want a reboot because the network changed", conv.condition)
	}
}

func TestDecideConvergenceStagesAWirelessEdit(t *testing.T) {
	// A wireless interface joins its network at boot.
	m := labMachine()
	m.Spec.Network.Interfaces = append(m.Spec.Network.Interfaces, machine.InterfaceSpec{
		Name: "wlan0", Wireless: &machine.WirelessSpec{SSID: "homenet-guest"},
	})
	facts := labFacts()
	boot := labNetwork()
	boot.Interfaces = append(boot.Interfaces, machine.InterfaceSpec{
		Name: "wlan0", Wireless: &machine.WirelessSpec{SSID: "homenet"},
	})
	facts.Boot.Network = &boot
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("the condition is %+v, want reason RebootPending", conv.condition)
	}
	if !conv.stage || conv.requestLoad {
		t.Errorf("a wireless edit stages and waits for a reboot: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "homenet-guest") {
		t.Errorf("the message is %q, want it to contain the diff", conv.condition.Message)
	}
}

func TestDecideConvergenceRefusesAnSSIDThatCannotNameAFile(t *testing.T) {
	// Init reads the passphrase from a file named after the SSID.
	m := labMachine()
	m.Spec.Network.Interfaces = append(m.Spec.Network.Interfaces, machine.InterfaceSpec{
		Name: "wlan0", Wireless: &machine.WirelessSpec{SSID: "../secrets"},
	})
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "StagingRejected" {
		t.Fatalf("the condition is %+v, want reason StagingRejected", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("the convergence stages an invalid network, want no staging")
	}
}

func TestDecideConvergenceRefusesTwoInterfacesForOnePort(t *testing.T) {
	m := labMachine()
	m.Spec.Network.Interfaces = append(m.Spec.Network.Interfaces, machine.InterfaceSpec{Name: "eth0"})
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "StagingRejected" {
		t.Fatalf("the condition is %+v, want reason StagingRejected", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("the convergence stages an invalid network, want no staging")
	}
	if !strings.Contains(conv.condition.Message, "remove one of them") {
		t.Errorf("the message is %q, want it to name the fix", conv.condition.Message)
	}
}

func TestDecideConvergenceIgnoresANetworkTheBootNeverRecorded(t *testing.T) {
	// Treating a missing record as an empty network would stage a
	// reboot on every machine that declares an interface.
	facts := labFacts()
	facts.Boot.Network = nil
	m := labMachine()
	m.Spec.Network.Interfaces[0].Nameservers = []string{"10.10.0.1"}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "Converged" {
		t.Errorf("an unrecorded network is not drift: %+v", conv.condition)
	}
}

func TestValidateStagingAcceptsAGrow(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "3Gi"
	if err := validateStaging(spec, labFacts()); err != nil {
		t.Error(err)
	}
}

func TestValidateStagingRefusesAShrink(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "1Gi" // the partition is already 2Gi
	err := validateStaging(spec, labFacts())
	if err == nil {
		t.Fatal("validateStaging accepted a size below the partition, want an error")
	}
	if !strings.Contains(err.Error(), "can only grow") {
		t.Errorf("the error is %v, want it to state that a role can only grow", err)
	}
	if !strings.Contains(err.Error(), "2Gi or more") {
		t.Errorf("the error is %v, want it to name the size 2Gi", err)
	}
	if !strings.Contains(err.Error(), "Machine document") {
		t.Errorf("the error is %v, want it to name the Machine document", err)
	}
}

func TestValidateStagingRefusesFixingARemainderBelowItsSize(t *testing.T) {
	// clusterState has no size and its partition is 1Gi.
	spec := labStorage()
	spec.ClusterState.Size = "512Mi"
	if err := validateStaging(spec, labFacts()); err == nil {
		t.Error("validateStaging accepted a size below the partition of a role with no size, want an error")
	}
}

func TestValidateStagingRefusesAnUnparseableSize(t *testing.T) {
	spec := labStorage()
	spec.PodStorage.Size = "quite-large"
	if err := validateStaging(spec, labFacts()); err == nil {
		t.Error("validateStaging accepted the size \"quite-large\", want an error")
	}
}

func TestValidateStagingRefusesUnknownDevicesForNewRoles(t *testing.T) {
	facts := labFacts()
	facts.Storage.PodStorage = machine.StorageRoleStatus{Backing: machine.BackingMemory}
	spec := labStorage()
	spec.PodStorage.Device = "/dev/vdz"
	err := validateStaging(spec, facts)
	if err == nil {
		t.Fatal("validateStaging accepted /dev/vdz, which is not a block device of the machine, want an error")
	}
	for _, want := range []string{"/dev/vdz", "vda, vdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %v, want it to contain %q", err, want)
		}
	}
}

func TestRenderManifestIsDeterministicAndCarriesNoStatus(t *testing.T) {
	m := labMachine()
	m.Status.Version.Liken = "should-not-appear"
	a, hashA, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	_, hashB, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Error("one spec rendered to different bytes, want the same bytes")
	}
	if strings.Contains(string(a), "status") || strings.Contains(string(a), "should-not-appear") {
		t.Errorf("the staged manifest has a status, want the spec only:\n%s", a)
	}
	if parsed, err := machine.Parse(a); err != nil || parsed.Metadata.Name != "liken-dev" {
		t.Errorf("parsing the rendered manifest: %v", err)
	}
}

func TestDecideConvergenceWithoutFacts(t *testing.T) {
	conv := decideConvergence(labMachine(), nil, nil, "", turnStandalone)
	if conv.condition.Status != "Unknown" || conv.condition.Reason != "FactsIncomplete" {
		t.Errorf("the condition is %+v, want reason FactsIncomplete", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("no side effects without facts")
	}
}

func TestDecideConvergenceWithoutABootRecord(t *testing.T) {
	facts := labFacts()
	facts.Boot = machine.BootStatus{}
	conv := decideConvergence(labMachine(), facts, nil, "", turnStandalone)
	if conv.condition.Reason != "FactsIncomplete" {
		t.Errorf("the condition is %+v, want reason FactsIncomplete", conv.condition)
	}
}

func TestDecideConvergenceWhenTheBootIsCurrent(t *testing.T) {
	conv := decideConvergence(labMachine(), labFacts(), nil, "", turnStandalone)
	if conv.condition.Status != "True" || conv.condition.Reason != "Converged" {
		t.Errorf("the condition is %+v, want status True and reason Converged", conv.condition)
	}
	if conv.stage || conv.requestReboot || conv.withdraw || conv.clearRejection {
		t.Error("a converged machine needs no side effects")
	}
}

func TestDecideConvergenceWithdrawsAStagedManifestNobodyWants(t *testing.T) {
	// The spec was edited and reverted before a reboot, so the edit
	// is still staged and the next boot would apply it.
	conv := decideConvergence(labMachine(), labFacts(), nil, "some-staged-hash", turnStandalone)
	if conv.condition.Reason != "Converged" {
		t.Fatalf("the condition is %+v, want reason Converged", conv.condition)
	}
	if !conv.withdraw {
		t.Error("the staged manifest stays, want it withdrawn because the spec matches this boot again")
	}
	if conv.stage || conv.requestReboot {
		t.Errorf("withdrawal is the only side effect here: %+v", conv)
	}
}

func TestDecideConvergenceClearsARejectionOnceTheSpecMovesOn(t *testing.T) {
	// Init rejected an edit at boot, and the spec was then reverted
	// to what the machine runs.
	rejection := &machine.Rejection{Hash: "the-abandoned-spec", Reason: "could not grow"}
	conv := decideConvergence(labMachine(), labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "Converged" {
		t.Fatalf("the condition is %+v, want reason Converged", conv.condition)
	}
	if !conv.clearRejection {
		t.Error("the rejection stays, want it cleared because the spec changed")
	}
}

// grownLabMachine drifts only in podStorage, grown to 3Gi.
func grownLabMachine() *machine.Machine {
	m := labMachine()
	m.Spec.Storage.PodStorage.Size = "3Gi"
	return m
}

func TestDecideConvergenceStagesAndWaitsUnderManualPolicy(t *testing.T) {
	m := grownLabMachine()
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" {
		t.Fatalf("the condition is %+v, want reason RebootPending", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("Manual policy stages but never reboots: %+v", conv)
	}
	if !strings.Contains(conv.condition.Message, "podStorage") {
		t.Errorf("the message is %q, want it to contain the diff", conv.condition.Message)
	}
}

func TestDecideConvergenceRequestsARebootUnderAutoPolicy(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "RebootRequested" {
		t.Fatalf("the condition is %+v, want reason RebootRequested", conv.condition)
	}
	if !conv.stage || !conv.requestReboot {
		t.Errorf("Auto policy stages and reboots: %+v", conv)
	}
}

func TestDecideConvergenceIsIdempotentAboutStaging(t *testing.T) {
	m := grownLabMachine()
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	conv := decideConvergence(m, labFacts(), nil, hash, turnStandalone)
	if conv.stage {
		t.Error("the convergence stages bytes that are already staged, want no staging")
	}
	if conv.condition.Reason != "RebootPending" {
		t.Errorf("the condition still reports the pending reboot: %+v", conv.condition)
	}
}

func TestDecideConvergenceHonorsARejection(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	rejection := &machine.Rejection{Hash: hash, Reason: "disk on fire"}

	conv := decideConvergence(m, labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "RejectedLastBoot" {
		t.Fatalf("the condition is %+v, want reason RejectedLastBoot", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("the convergence stages or reboots for a rejected spec, want neither")
	}
	if !strings.Contains(conv.condition.Message, "disk on fire") {
		t.Errorf("the message is %q, want it to contain init's reason", conv.condition.Message)
	}
}

func TestDecideConvergenceStagesAgainForADifferentEdit(t *testing.T) {
	m := grownLabMachine()
	rejection := &machine.Rejection{Hash: "some-other-hash", Reason: "old news"}
	conv := decideConvergence(m, labFacts(), rejection, "", turnStandalone)
	if conv.condition.Reason != "RebootPending" || !conv.stage {
		t.Errorf("the convergence is %+v, want the different spec staged", conv)
	}
}

func TestDecideConvergenceRefusesToLoopOnAContradiction(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	_, hash, err := renderManifest(m.Metadata.Name, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	facts := labFacts()
	facts.Boot.ManifestHash = hash // the facts say this spec was actuated, but drift remains

	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "BootMismatch" {
		t.Fatalf("the condition is %+v, want reason BootMismatch", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("the convergence stages or reboots for a BootMismatch, want neither")
	}
}

func TestDecideConvergenceNeedsADurableMachineState(t *testing.T) {
	m := grownLabMachine()
	facts := labFacts()
	facts.Storage.MachineState = machine.StorageRoleStatus{Backing: machine.BackingMemory}
	conv := decideConvergence(m, facts, nil, "", turnStandalone)
	if conv.condition.Reason != "MachineStateEphemeral" {
		t.Fatalf("the condition is %+v, want reason MachineStateEphemeral", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("nowhere durable to stage means no side effects")
	}
}

func TestDecideConvergenceRefusesInvalidStaging(t *testing.T) {
	m := labMachine()
	m.Spec.Storage.PodStorage.Size = "1Gi" // a shrink
	conv := decideConvergence(m, labFacts(), nil, "", turnStandalone)
	if conv.condition.Reason != "StagingRejected" {
		t.Fatalf("the condition is %+v, want reason StagingRejected", conv.condition)
	}
	if conv.stage || conv.requestReboot {
		t.Error("the convergence stages an invalid spec, want no staging")
	}
	if !strings.Contains(conv.condition.Message, "can only grow") {
		t.Errorf("the message is %q, want it to contain the validation error", conv.condition.Message)
	}
}

func TestDecideConvergenceWaitsForATurnAsAClusterMember(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnAwaiting)
	if conv.condition.Reason != "AwaitingTurn" {
		t.Fatalf("the condition is %+v, want reason AwaitingTurn", conv.condition)
	}
	if !conv.stage || conv.requestReboot {
		t.Errorf("an ungranted member stages but never reboots: %+v", conv)
	}
}

func TestDecideConvergenceRebootsWhenTheMachineHasATurn(t *testing.T) {
	m := grownLabMachine()
	m.Spec.RebootPolicy = machine.RebootAuto
	conv := decideConvergence(m, labFacts(), nil, "", turnGranted)
	if conv.condition.Reason != "RebootRequested" || !conv.requestReboot {
		t.Fatalf("the condition is %+v, want RebootRequested because the machine has a turn", conv.condition)
	}
}

func TestDecideConvergenceManualPolicyStaysPendingWithATurn(t *testing.T) {
	m := grownLabMachine()
	conv := decideConvergence(m, labFacts(), nil, "", turnGranted)
	if conv.condition.Reason != "RebootPending" || conv.requestReboot {
		t.Fatalf("rebootPolicy: Manual waits for a person even when the machine has a turn: %+v", conv.condition)
	}
}

// gateForTest returns a convergence with only a hash, which is all
// that gateDisruption reads.
func gateForTest() convergence {
	return convergence{hash: "3943abfa6adf0123456789abcdef0123456789abcdef0123456789abcdef0123"}
}

func TestManualWithAMatchingApprovalTakesTheAutoPath(t *testing.T) {
	c := gateForTest()
	gateDisruption(&c, "SpecConverged", machine.RebootManual, turnAwaiting, false,
		gateForTest().hash, "a summary", "pending msg", "awaiting msg", "requested msg")
	if c.condition.Reason != "AwaitingTurn" {
		t.Fatalf("the reason is %s, want AwaitingTurn for an approved Manual cluster member", c.condition.Reason)
	}
	if c.requestReboot {
		t.Fatal("requestReboot is true, want false before cluster-operator grants a turn")
	}
}

func TestManualApprovedAndGrantedRequestsTheDisruption(t *testing.T) {
	c := gateForTest()
	gateDisruption(&c, "SpecConverged", machine.RebootManual, turnGranted, false,
		gateForTest().hash, "a summary", "pending msg", "awaiting msg", "requested msg")
	if c.condition.Reason != "RebootRequested" || !c.requestReboot {
		t.Fatalf("the reason is %s, want RebootRequested for an approved Manual machine with a turn", c.condition.Reason)
	}
}

func TestManualWithAMismatchedApprovalStaysPending(t *testing.T) {
	c := gateForTest()
	gateDisruption(&c, "SpecConverged", machine.RebootManual, turnAwaiting, false,
		"deadbeefdead", "a summary", "pending msg", "awaiting msg", "requested msg")
	if c.condition.Reason != "RebootPending" || c.condition.Message != "pending msg" {
		t.Fatalf("the condition is %s: %s, want RebootPending because the approval names another hash", c.condition.Reason, c.condition.Message)
	}
}

func TestManualWithNoApprovalStaysPending(t *testing.T) {
	c := gateForTest()
	gateDisruption(&c, "SpecConverged", machine.RebootManual, turnAwaiting, false,
		"", "a summary", "pending msg", "awaiting msg", "requested msg")
	if c.condition.Reason != "RebootPending" || c.condition.Message != "pending msg" {
		t.Fatalf("the condition is %s: %s, want RebootPending: pending msg", c.condition.Reason, c.condition.Message)
	}
}

func TestAutoWithoutApprovalAwaitsTurn(t *testing.T) {
	c := gateForTest()
	gateDisruption(&c, "SpecConverged", machine.RebootAuto, turnAwaiting, false,
		"", "a summary", "pending msg", "awaiting msg", "requested msg")
	if c.condition.Reason != "AwaitingTurn" {
		t.Fatalf("the reason is %s, want AwaitingTurn", c.condition.Reason)
	}
}

func TestEveryGateRecordsAPendingEntry(t *testing.T) {
	for _, tc := range []struct {
		policy machine.RebootPolicy
		t      turn
	}{
		{machine.RebootManual, turnAwaiting},
		{machine.RebootAuto, turnAwaiting},
		{machine.RebootAuto, turnGranted},
	} {
		c := gateForTest()
		gateDisruption(&c, "CredentialsConverged", tc.policy, tc.t, true,
			"", "registry credentials for 2 hosts", "p", "a", "r")
		if c.pending == nil {
			t.Fatalf("policy %s turn %d: no pending entry", tc.policy, tc.t)
		}
		if c.pending.Kind != machine.DisruptionRestart ||
			c.pending.Condition != "CredentialsConverged" ||
			c.pending.Hash != c.hash ||
			c.pending.Summary != "registry credentials for 2 hosts" {
			t.Fatalf("wrong entry: %+v", *c.pending)
		}
	}
}
