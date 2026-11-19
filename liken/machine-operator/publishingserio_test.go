package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// pulse8Line is the Pulse-Eight's communications interface, which
// cdc_acm binds and which has the tty.
var pulse8Line = hardware.Device{
	Bus: "usb", Address: "1-4:1.0", Driver: "cdc_acm", ClassCode: "02",
	Vendor: "2548", Product: "1002",
}

// attachedDelivery is what the delivery walk finds under the interface
// after init attaches the line: the tty, the CEC adapter under the
// port, and the remote's input event node under the adapter.
var attachedDelivery = hardware.Delivery{
	Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyACM0", Subsystem: "tty"},
		{Path: "/dev/cec0", Subsystem: "cec"},
		{Path: "/dev/input/event13", Subsystem: "input"},
	},
	BusNode: "/dev/bus/usb/001/004",
}

// An attached line publishes the CEC node and the input node and
// neither the tty nor the usbfs node.
func TestASerioAttachmentPublishesTheBusAndTheRemote(t *testing.T) {
	published := publishFor(pulse8Line, attachedDelivery, []machine.SerioAttachment{pulse8Serio})

	want := []publishedDevice{
		{Suffix: "-cec", Subsystem: "cec", Nodes: []string{"/dev/cec0"}},
		{Suffix: "-input", Subsystem: "input", Nodes: []string{"/dev/input/event13"}},
	}
	if !slices.EqualFunc(published, want, samePublished) {
		t.Errorf("the devices are %+v, want %+v", published, want)
	}
}

func TestASerioLinePublishesNothingBeforeTheAttachment(t *testing.T) {
	tty := hardware.Delivery{
		Nodes:   []hardware.DeliveredNode{{Path: "/dev/ttyACM0", Subsystem: "tty"}},
		BusNode: "/dev/bus/usb/001/004",
	}
	if published := publishFor(pulse8Line, tty, []machine.SerioAttachment{pulse8Serio}); len(published) != 0 {
		t.Errorf("the devices are %+v, want none before the attachment", published)
	}
}

// pulse8HID is the adapter's HID interface, for its keyboard mode. It
// has the adapter's vendor and product and no serial line.
var pulse8HID = hardware.Device{Bus: "usb", Address: "1-4:1.2", Driver: "usbhid", Vendor: "2548", Product: "1002"}

var pulse8HIDDelivery = hardware.Delivery{Nodes: []hardware.DeliveredNode{
	{Path: "/dev/hidraw0", Subsystem: "hidraw"},
}, BusNode: "/dev/bus/usb/001/004"}

// Without a spec.serio entry, the adapter is an ordinary serial line
// and publishes as any other device.
func TestTheSerioShapeNeedsAnEntry(t *testing.T) {
	got := publishFor(pulse8Line, attachedDelivery, nil)
	want := publishDevices(pulse8Line, attachedDelivery)
	if !slices.EqualFunc(got, want, samePublished) {
		t.Errorf("the devices are %+v, want %+v, the devices of publishDevices", got, want)
	}
}

// With an entry, the HID interface keeps its device and loses the
// usbfs node. Without an entry, it keeps the node.
func TestASiblingInterfaceOfAnAttachedLineLosesTheUsbfsNode(t *testing.T) {
	tests := []struct {
		name  string
		serio []machine.SerioAttachment
		want  []string
	}{
		{"an entry", []machine.SerioAttachment{pulse8Serio}, []string{"/dev/hidraw0"}},
		{"no entry", nil, []string{"/dev/hidraw0", "/dev/bus/usb/001/004"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := publishFor(pulse8HID, pulse8HIDDelivery, test.serio)
			if len(got) != 1 || !slices.Equal(got[0].Nodes, test.want) || got[0].Subsystem != "hidraw" {
				t.Errorf("the devices are %+v, want one hidraw device with nodes %v", got, test.want)
			}
		})
	}
}

func samePublished(a, b publishedDevice) bool {
	return a.Suffix == b.Suffix && a.Subsystem == b.Subsystem && slices.Equal(a.Nodes, b.Nodes) &&
		a.RenderNode == b.RenderNode && a.DisplayNode == b.DisplayNode && a.Shareable == b.Shareable
}

// Both devices have the adapter's attributes, so a claim can pair them
// by address, and neither allows a second allocation.
func TestTheInventoryPublishesBothSerioDevicesExclusive(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{pulse8Line}, delivering(attachedDelivery), noRoles,
		[]machine.SerioAttachment{pulse8Serio})

	var names, subsystems []string
	for _, d := range devices {
		names = append(names, d.Name)
		subsystems = append(subsystems, stringAttribute(d, "subsystem"))
		if d.AllowMultipleAllocations != nil || stringAttribute(d, "address") != "1-4:1.0" {
			t.Errorf("device %s is %+v, want address 1-4:1.0 and no multiple allocations", d.Name, d)
		}
	}
	if !slices.Equal(names, []string{"usb-1-4-1-0-cec", "usb-1-4-1-0-input"}) ||
		!slices.Equal(subsystems, []string{"cec", "input"}) {
		t.Errorf("device names are %v and subsystems are %v, want usb-1-4-1-0-cec and usb-1-4-1-0-input with cec and input", names, subsystems)
	}
}

// stringAttribute returns one string attribute of a slice device, or
// "" when the device has no such attribute.
func stringAttribute(d kubernetes.SliceDevice, name string) string {
	if a, ok := d.Attributes[name]; ok && a.String != nil {
		return *a.String
	}
	return ""
}

// A line whose entry is removed from the spec stays attached until the
// next boot, so serioInEffect returns the entries of the spec and of
// the boot record.
func TestDeclaredSerioJoinsTheSpecAndTheBootRecord(t *testing.T) {
	withSerial := pulse8Serio
	withSerial.USB.Serial = "A1"
	facts := &machine.MachineStatus{Boot: machine.BootStatus{Serio: []machine.SerioAttachment{withSerial}}}

	got := serioInEffect([]machine.SerioAttachment{pulse8Serio}, facts)
	nilFacts := serioInEffect([]machine.SerioAttachment{pulse8Serio}, nil)

	if !slices.Equal(got, []machine.SerioAttachment{pulse8Serio, withSerial}) ||
		!slices.Equal(nilFacts, []machine.SerioAttachment{pulse8Serio}) {
		t.Errorf("serioInEffect with a boot record is %v and without is %v, want the spec and boot entries, then the spec entry alone", got, nilFacts)
	}
}

func TestDeclaredSerioIsWhatWasLastSet(t *testing.T) {
	t.Cleanup(func() { setDeclaredSerio(nil) })
	setDeclaredSerio([]machine.SerioAttachment{pulse8Serio})

	if got := declaredSerio(); !slices.Equal(got, []machine.SerioAttachment{pulse8Serio}) {
		t.Errorf("declaredSerio() = %v, want the entry that setDeclaredSerio stored", got)
	}
}

// While an allocated serio device is absent, its claim's CDI spec names
// a node that does not exist, so a container fails to start and does
// not open another device that took the old node's device number.
func TestAnAbsentSerioDeviceFailsClosed(t *testing.T) {
	old := cdiDir
	cdiDir = t.TempDir()
	t.Cleanup(func() { cdiDir = old })
	devices := []cdiDevice{
		{Name: "claim-1-usb-1-4-1-0-cec", ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/cec0"})}},
		{Name: "claim-1-usb-1-4-1-0-input", ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/input/event13"})}},
		{Name: "claim-1-usb-2-1-1-0", ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/sda"})}},
	}
	if err := writeCDISpec("claim-1", devices); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	if err := refreshCDISpec(t.TempDir(), "claim-1", map[string]hardware.Device{}, nil, machineEvents{}); err != nil {
		t.Fatalf("refreshing the CDI spec: %v", err)
	}

	raw, err := os.ReadFile(cdiSpecPath("claim-1"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decoding the CDI spec: %v", err)
	}
	want := [][]string{{serioAbsentNode}, {serioAbsentNode}, {deviceAbsentNode}}
	for i, device := range spec.Devices {
		var paths []string
		for _, node := range device.ContainerEdits.DeviceNodes {
			paths = append(paths, node.Path)
			if node.Major != 0 {
				t.Errorf("%s has the device number of %+v, want none", device.Name, node)
			}
		}
		if !slices.Equal(paths, want[i]) {
			t.Errorf("%s has nodes %v, want %v", device.Name, paths, want[i])
		}
	}
}

// A claim allocated to the interface's bare name before the entry
// existed has the tty and the usbfs node in its CDI spec. TIOCSETD on
// the tty and USBDEVFS_RESET on the usbfs node each end the attachment,
// so the spec fails closed.
func TestAClaimOnTheTTYFromBeforeTheEntryFailsClosed(t *testing.T) {
	old := cdiDir
	cdiDir = t.TempDir()
	t.Cleanup(func() { cdiDir = old })
	t.Cleanup(func() { setDeclaredSerio(nil) })
	setDeclaredSerio([]machine.SerioAttachment{pulse8Serio})
	devices := []cdiDevice{{
		Name:           "claim-1-usb-1-4-1-0",
		ContainerEdits: cdiEdits{DeviceNodes: deviceNodes([]string{"/dev/ttyACM0", "/dev/bus/usb/001/004"})},
	}}
	if err := writeCDISpec("claim-1", devices); err != nil {
		t.Fatalf("writing the CDI spec: %v", err)
	}

	byName := map[string]hardware.Device{deviceName(pulse8Line): pulse8Line}
	if err := refreshCDISpec(t.TempDir(), "claim-1", byName, nil, machineEvents{}); err != nil {
		t.Fatalf("refreshing the CDI spec: %v", err)
	}

	raw, err := os.ReadFile(cdiSpecPath("claim-1"))
	if err != nil {
		t.Fatalf("reading the CDI spec: %v", err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decoding the CDI spec: %v", err)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != serioAbsentNode {
		t.Errorf("the nodes are %+v, want only %s", nodes, serioAbsentNode)
	}
}
