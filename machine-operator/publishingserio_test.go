package main

import (
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

// The fourth examined shape: the tty is never published, the CEC node
// is the primary, and the input node is an exclusive companion. The
// usbfs node is delivered nowhere, because through it a claimant could
// detach cdc_acm and end the port under the other claim.
func TestASerioAttachmentPublishesTheBusAndTheRemote(t *testing.T) {
	published := publishFor(pulse8Line, attachedDelivery, []machine.SerioAttachment{pulse8Serio})

	want := []publishedDevice{
		{Subsystem: "cec", Nodes: []string{"/dev/cec0"}},
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

// Without an entry, the adapter is an ordinary serial line and keeps
// the default. The adapter's HID interface carries no tty, so an entry
// leaves it as it was.
func TestTheSerioShapeNeedsAnEntryAndASerialLine(t *testing.T) {
	hid := hardware.Device{Bus: "usb", Address: "1-4:1.2", Driver: "usbhid", Vendor: "2548", Product: "1002"}
	hidDelivery := hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/hidraw0", Subsystem: "hidraw"},
	}, BusNode: "/dev/bus/usb/001/004"}
	tests := []struct {
		name     string
		device   hardware.Device
		delivery hardware.Delivery
		serio    []machine.SerioAttachment
	}{
		{"no entry", pulse8Line, attachedDelivery, nil},
		{"the HID interface", hid, hidDelivery, []machine.SerioAttachment{pulse8Serio}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := publishFor(test.device, test.delivery, test.serio)
			want := publishDevices(test.device, test.delivery)
			if !slices.EqualFunc(got, want, samePublished) {
				t.Errorf("published = %+v, want %+v", got, want)
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
	if !slices.Equal(names, []string{"usb-1-4-1-0", "usb-1-4-1-0-input"}) ||
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
