package main

// No claim can receive a disk that backs a storage role. When the facts
// cannot be read, the operator cannot tell which disks back a role, so
// it offers no disk at all.

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	drav1 "k8s.io/kubelet/pkg/apis/dra/v1"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// protecting sets platformProtection for one test.
func protecting(t *testing.T, p protection) {
	t.Helper()
	saved := platformProtection.Load()
	t.Cleanup(func() { platformProtection.Store(saved) })
	setPlatformProtection(p)
}

// memoryBacked returns complete facts in which every role is backed by
// memory, so no disk is protected.
func memoryBacked() *machine.MachineStatus {
	facts := &machine.MachineStatus{}
	for _, name := range machine.StorageRoleNames {
		facts.Storage.Role(name).Backing = machine.BackingMemory
	}
	return facts
}

func diskAndGPU() ([]hardware.Device, func(hardware.Device) hardware.Delivery) {
	devices := []hardware.Device{
		{Bus: "pci", Address: "0000:00:06.0", Driver: "virtio-pci", Class: "storage"},
		{Bus: "pci", Address: "0000:00:02.0", Driver: "i915", Class: "display"},
	}
	inspect := func(d hardware.Device) hardware.Delivery {
		if d.Driver == "i915" {
			return hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/dri/renderD128", Subsystem: "drm"}}}
		}
		return hardware.Delivery{Nodes: []hardware.DeliveredNode{
			{Path: "/dev/vda", Subsystem: "block", Block: "vda"},
			{Path: "/dev/vda1", Subsystem: "block", Block: "vda1"},
		}}
	}
	return devices, inspect
}

func sliceNames(devices []kubernetes.SliceDevice) []string {
	var names []string
	for _, d := range devices {
		names = append(names, d.Name)
	}
	return names
}

func TestTheInventoryOffersNoDiskWhileTheRolesAreUnknown(t *testing.T) {
	devices, inspect := diskAndGPU()

	got := sliceNames(inventoryDevices(devices, inspect, protectionOf(nil), nil))

	if !slices.Equal(got, []string{"pci-0000-00-02-0"}) {
		t.Errorf("devices = %v, want the GPU alone: no facts confirm which disks the machine uses", got)
	}
}

func TestAMemoryBackedMachineOffersItsDisks(t *testing.T) {
	devices, inspect := diskAndGPU()

	got := sliceNames(inventoryDevices(devices, inspect, protectionOf(memoryBacked()), nil))

	if !slices.Equal(got, []string{"pci-0000-00-02-0", "pci-0000-00-06-0"}) {
		t.Errorf("devices = %v, want both: the facts name no disk-backed role", got)
	}
}

// A slice that offered a disk while the facts were readable no longer
// offers it after the first pass that cannot read them.
func TestAPassWithoutFactsWithdrawsAnOfferedDisk(t *testing.T) {
	newDRAFixture(t)
	client, _ := passClients(t, newPassAPI())
	o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
	mm := newMachineMetrics(o, &fetcher{})
	node := &nodeObject{}
	node.Metadata.Name, node.Metadata.UID = "node-1", "uid-node-1"
	r := &reader{client: client}
	published := func() []string {
		t.Helper()
		slice := &kubernetes.ResourceSlice{}
		if err := client.RequestJSON(http.MethodGet, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, slice); err != nil {
			return nil
		}
		return sliceNames(slice.Spec.Devices)
	}

	_ = publishDeviceInventory(r, node, memoryBacked(), nil, mm)
	before := published()
	_ = publishDeviceInventory(r, node, nil, nil, mm)
	after := published()

	if !slices.Equal(before, []string{"usb-2-1-1-0"}) || len(after) != 0 {
		t.Errorf("the slice offered %v with facts and %v without, want the stick and then nothing", before, after)
	}
}

// prepareStick runs the kubelet's prepare call for the fixture's claim
// and returns the claim's error message, which is empty on success.
func prepareStick(t *testing.T, fixture *draFixture) string {
	t.Helper()
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatalf("calling NodePrepareResources: %v", err)
	}
	return resp.Claims["claim-1"].Error
}

// An allocation can outlive the slice it came from, so prepare checks
// the disk again.
func TestPrepareRefusesADiskThatBacksARole(t *testing.T) {
	fixture := newDRAFixture(t)
	facts := memoryBacked()
	facts.Storage.Role(machine.StorageRoleNames[0]).Device = "sda"
	protecting(t, protectionOf(facts))

	if prepareStick(t, fixture) == "" {
		t.Error("prepare returned no error, want a refusal of a disk that backs a storage role")
	}
}

func TestPrepareRefusesADiskWhileTheRolesAreUnknown(t *testing.T) {
	fixture := newDRAFixture(t)
	protecting(t, protectionOf(nil))

	if prepareStick(t, fixture) == "" {
		t.Error("prepare returned no error, want a refusal while no facts show which disks the machine uses")
	}
}

// A device with no block node has nothing that a storage role can use,
// so unreadable facts do not withhold it.
func TestPrepareDeliversAGPUWhileTheRolesAreUnknown(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0"
	protecting(t, protectionOf(nil))

	if message := prepareStick(t, fixture); message != "" {
		t.Errorf("prepare returned the error %q, want none for a GPU", message)
	}
}

// While the facts cannot be read, a prepared disk claim's CDI spec
// lists withheldNode in place of its nodes. When the facts can be read
// again, the spec lists the disk again.
func TestRefreshWithholdsAPreparedDiskUntilTheRolesAreKnown(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	protecting(t, protectionOf(nil))
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})
	withheld := specPaths(t, fixture, "claim-1")

	setPlatformProtection(protectionOf(memoryBacked()))
	refreshCDISpecs(draSysfsRoot, nil, machineEvents{})
	restored := specPaths(t, fixture, "claim-1")
	slices.Sort(restored)

	if !slices.Equal(withheld, []string{withheldNode}) {
		t.Errorf("paths = %v while the roles were unknown, want %s alone", withheld, withheldNode)
	}
	if !slices.Equal(restored, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("paths = %v once the roles were known, want the stick's nodes", restored)
	}
}

func TestTheFactsConditionReportsThatTheDisksAreWithheld(t *testing.T) {
	c := factsCondition(errFactsForTest)

	if c.Status != api.ConditionFalse || c.Message != "The device inventory publishes no disk until the facts can be read: no facts tree" {
		t.Errorf("condition is %+v, want False with the message that no disk is published until the facts can be read", c)
	}
}

var errFactsForTest = errors.New("no facts tree")
