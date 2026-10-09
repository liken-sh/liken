package main

// The machine's own disks stay out of every claim, and a disk whose
// storage role the operator cannot confirm stays out with them: the
// facts are the only record of which partitions back a role, so a
// machine whose facts do not read offers no disk at all.

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

// protecting sets the protection the DRA plugin and the CDI refresh
// read, the way a pass sets it from the facts, for one test.
func protecting(t *testing.T, p protection) {
	t.Helper()
	saved := platformProtection.Load()
	t.Cleanup(func() { platformProtection.Store(saved) })
	setPlatformProtection(p)
}

// memoryBacked is the facts of a machine whose every role is in
// memory: a complete record that names no disk.
func memoryBacked() *machine.MachineStatus {
	facts := &machine.MachineStatus{}
	for _, name := range machine.StorageRoleNames {
		facts.Storage.Role(name).Backing = machine.BackingMemory
	}
	return facts
}

// diskAndGPU is a disk and a GPU, each with the delivery its driver
// gives it.
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

// A slice that offered a disk while the facts read loses the disk on
// the first pass that cannot read them.
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
// and answers its error.
func prepareStick(t *testing.T, fixture *draFixture) string {
	t.Helper()
	resp, err := fixture.plugin.NodePrepareResources(t.Context(), &drav1.NodePrepareResourcesRequest{
		Claims: []*drav1.Claim{{Namespace: "media", Name: "stick", Uid: "claim-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Claims["claim-1"].Error
}

// An allocation can outlive the offer it came from, so prepare checks
// the disk again, whatever the slice said.
func TestPrepareRefusesADiskThatBacksARole(t *testing.T) {
	fixture := newDRAFixture(t)
	facts := memoryBacked()
	facts.Storage.Role(machine.StorageRoleNames[0]).Device = "sda"
	protecting(t, protectionOf(facts))

	if prepareStick(t, fixture) == "" {
		t.Error("prepare delivered a disk that backs a storage role")
	}
}

func TestPrepareRefusesADiskWhileTheRolesAreUnknown(t *testing.T) {
	fixture := newDRAFixture(t)
	protecting(t, protectionOf(nil))

	if prepareStick(t, fixture) == "" {
		t.Error("prepare delivered a disk while no facts confirmed the machine does not use it")
	}
}

// A device with no block node holds nothing a storage role can use,
// so unknown roles do not stop it.
func TestPrepareDeliversAGPUWhileTheRolesAreUnknown(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.addGPU(t)
	fixture.allocated = "pci-0000-00-02-0"
	protecting(t, protectionOf(nil))

	if message := prepareStick(t, fixture); message != "" {
		t.Errorf("prepare refused the GPU: %s", message)
	}
}

// A prepared disk claim whose disk the operator can no longer confirm
// names a node that does not exist, so the next container that holds
// the claim fails to start. When the facts read again, the claim gets
// its disk back.
func TestRefreshWithholdsAPreparedDiskUntilTheRolesAreKnown(t *testing.T) {
	fixture := newDRAFixture(t)
	fixture.enumerate(t, 4)
	prepared(t, fixture)

	protecting(t, protectionOf(nil))
	refreshCDISpecs(draSysfsRoot, nil)
	withheld := specPaths(t, fixture, "claim-1")

	setPlatformProtection(protectionOf(memoryBacked()))
	refreshCDISpecs(draSysfsRoot, nil)
	restored := specPaths(t, fixture, "claim-1")
	slices.Sort(restored)

	if !slices.Equal(withheld, []string{withheldNode}) {
		t.Errorf("paths = %v while the roles were unknown, want %s alone", withheld, withheldNode)
	}
	if !slices.Equal(restored, []string{"/dev/bus/usb/002/004", "/dev/sda"}) {
		t.Errorf("paths = %v once the roles were known, want the stick's nodes", restored)
	}
}

func TestTheFactsConditionSaysTheDisksAreWithheld(t *testing.T) {
	c := factsCondition(errFactsForTest)

	if c.Status != api.ConditionFalse || c.Message != "no facts tree; the device inventory offers no disk until the facts read" {
		t.Errorf("condition = %+v", c)
	}
}

var errFactsForTest = errors.New("no facts tree")
