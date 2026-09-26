package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/liken-sh/liken/hardware"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// pulse8Line is the Pulse-Eight's communications interface, the one
// that owns the tty.
var pulse8Line = hardware.Device{
	Bus: "usb", Address: "1-4:1.0", Driver: "cdc_acm", ClassCode: "02",
	Vendor: "2548", Product: "1002",
}

// attachedDelivery is what the delivery walk finds under the interface
// once init holds the attachment: the tty, the CEC adapter under the
// port, and the remote's event node under the adapter.
var attachedDelivery = hardware.Delivery{
	Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyACM0", Subsystem: "tty"},
		{Path: "/dev/cec0", Subsystem: "cec"},
		{Path: "/dev/input/event13", Subsystem: "input"},
	},
	BusNode: "/dev/bus/usb/001/004",
}

// The fourth examined shape: the tty is never published, and the CEC
// node and the input node publish as two exclusive devices, each with
// a suffix. No device keeps the bare name, so a claim allocated to the
// tty before the entry was declared resolves to nothing. The usbfs
// node is delivered nowhere, because through it a claimant could
// detach cdc_acm and end the port under the other claim.
func TestASerioAttachmentPublishesTheBusAndTheRemote(t *testing.T) {
	published := publishFor(pulse8Line, attachedDelivery, []machine.SerioAttachment{pulse8Serio})

	want := []publishedDevice{
		{Suffix: "-cec", Subsystem: "cec", Nodes: []string{"/dev/cec0"}},
		{Suffix: "-input", Subsystem: "input", Nodes: []string{"/dev/input/event13"}},
	}
	if !slices.EqualFunc(published, want, samePublished) {
		t.Errorf("published = %+v", published)
	}
}

func TestASerioLinePublishesNothingBeforeTheAttachment(t *testing.T) {
	tty := hardware.Delivery{
		Nodes:   []hardware.DeliveredNode{{Path: "/dev/ttyACM0", Subsystem: "tty"}},
		BusNode: "/dev/bus/usb/001/004",
	}
	if published := publishFor(pulse8Line, tty, []machine.SerioAttachment{pulse8Serio}); len(published) != 0 {
		t.Errorf("published = %+v", published)
	}
}

// pulse8HID is the adapter's HID interface, which hid-generic binds
// for the firmware's keyboard mode. It has the adapter's USB identity
// and no serial line.
var pulse8HID = hardware.Device{Bus: "usb", Address: "1-4:1.2", Driver: "usbhid", Vendor: "2548", Product: "1002"}

var pulse8HIDDelivery = hardware.Delivery{Nodes: []hardware.DeliveredNode{
	{Path: "/dev/hidraw0", Subsystem: "hidraw"},
}, BusNode: "/dev/bus/usb/001/004"}

// Without an entry, the adapter is an ordinary serial line and keeps
// the default.
func TestTheSerioShapeNeedsAnEntry(t *testing.T) {
	got := publishFor(pulse8Line, attachedDelivery, nil)
	want := publishDevices(pulse8Line, attachedDelivery)
	if !slices.EqualFunc(got, want, samePublished) {
		t.Errorf("published = %+v, want %+v", got, want)
	}
}

// The HID interface keeps its own device, but not the usbfs node: the
// node opens the whole adapter, and a USBDEVFS_RESET through it would
// end the attachment under every CEC claim. Without an entry, the
// interface delivers the node as before.
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
				t.Errorf("published = %+v", got)
			}
		})
	}
}

func samePublished(a, b publishedDevice) bool {
	return a.Suffix == b.Suffix && a.Subsystem == b.Subsystem && slices.Equal(a.Nodes, b.Nodes) &&
		a.RenderNode == b.RenderNode && a.DisplayNode == b.DisplayNode && a.Shareable == b.Shareable
}

// Both devices carry the adapter's identity, so a claim pairs them by
// address, and neither allows a second allocation.
func TestTheInventoryPublishesBothSerioDevicesExclusive(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{pulse8Line}, delivering(attachedDelivery), nil,
		[]machine.SerioAttachment{pulse8Serio})

	var names, subsystems []string
	for _, d := range devices {
		names = append(names, d.Name)
		subsystems = append(subsystems, stringAttribute(d, "subsystem"))
		if d.AllowMultipleAllocations != nil || stringAttribute(d, "address") != "1-4:1.0" {
			t.Errorf("device = %+v", d)
		}
	}
	if !slices.Equal(names, []string{"usb-1-4-1-0-cec", "usb-1-4-1-0-input"}) ||
		!slices.Equal(subsystems, []string{"cec", "input"}) {
		t.Errorf("names = %v, subsystems = %v", names, subsystems)
	}
}

// stringAttribute reads one string attribute of a slice device, or ""
// when the device does not carry it.
func stringAttribute(d kubernetes.SliceDevice, name string) string {
	if a, ok := d.Attributes[name]; ok && a.String != nil {
		return *a.String
	}
	return ""
}

// A retracted entry's holder keeps the port until the next boot, so
// the boot record's entries count with the spec's.
func TestDeclaredSerioJoinsTheSpecAndTheBootRecord(t *testing.T) {
	withSerial := pulse8Serio
	withSerial.USB.Serial = "A1"
	facts := &machine.MachineStatus{Boot: machine.BootStatus{Serio: []machine.SerioAttachment{withSerial}}}

	got := serioInEffect([]machine.SerioAttachment{pulse8Serio}, facts)
	nilFacts := serioInEffect([]machine.SerioAttachment{pulse8Serio}, nil)

	if !slices.Equal(got, []machine.SerioAttachment{pulse8Serio, withSerial}) ||
		!slices.Equal(nilFacts, []machine.SerioAttachment{pulse8Serio}) {
		t.Errorf("got %v and %v", got, nilFacts)
	}
}

func TestDeclaredSerioIsWhatWasLastSet(t *testing.T) {
	t.Cleanup(func() { setDeclaredSerio(nil) })
	setDeclaredSerio([]machine.SerioAttachment{pulse8Serio})

	if got := declaredSerio(); !slices.Equal(got, []machine.SerioAttachment{pulse8Serio}) {
		t.Errorf("got %v", got)
	}
}

// While an allocated serio device is absent, or publishes nothing, its
// claim's spec names a node that does not exist, so a container that
// starts in that window fails to start instead of receiving whatever
// device took the old node's number meanwhile.
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
		t.Fatal(err)
	}

	if err := refreshCDISpec(t.TempDir(), "claim-1", map[string]hardware.Device{}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(cdiSpecPath("claim-1"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{serioAbsentNode}, {serioAbsentNode}, {"/dev/sda"}}
	for i, device := range spec.Devices {
		var paths []string
		for _, node := range device.ContainerEdits.DeviceNodes {
			paths = append(paths, node.Path)
			if node.Major != 0 {
				t.Errorf("%s names a device number: %+v", device.Name, node)
			}
		}
		if !slices.Equal(paths, want[i]) {
			t.Errorf("%s = %v", device.Name, paths)
		}
	}
}

// A claim allocated before the entry holds the interface's bare name,
// which was the tty's device, and its spec names the tty and the usbfs
// node. After the entry, that name resolves to nothing, and the old
// nodes are the two that end the attachment: TIOCSETD on the tty and
// USBDEVFS_RESET on the usbfs node. So the spec fails closed.
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
		t.Fatal(err)
	}

	byName := map[string]hardware.Device{deviceName(pulse8Line): pulse8Line}
	if err := refreshCDISpec(t.TempDir(), "claim-1", byName); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(cdiSpecPath("claim-1"))
	if err != nil {
		t.Fatal(err)
	}
	var spec cdiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	nodes := spec.Devices[0].ContainerEdits.DeviceNodes
	if len(nodes) != 1 || nodes[0].Path != serioAbsentNode {
		t.Errorf("nodes = %+v", nodes)
	}
}
