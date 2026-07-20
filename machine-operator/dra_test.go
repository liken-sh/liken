package main

// The DRA inventory mapping: which discovered devices become
// slice devices, what they are named, and which attributes they
// carry. The publish rule has three tests: driven and not part of
// the bus structure, deliverable, and not the platform's own disk.
// Each test has a case that refuses its counterexample.

import (
	"testing"

	"github.com/liken-sh/liken/hardware"
)

// delivering builds an inspect function that reports the same
// delivery for every device. It replaces the sysfs walk in these
// tests.
func delivering(d hardware.Delivery) func(hardware.Device) hardware.Delivery {
	return func(hardware.Device) hardware.Delivery { return d }
}

func TestInventoryPublishesDrivenDeliverableDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1:1.0", Driver: "uas", Modalias: "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
			Name: "QEMU QEMU USB HARDDRIVE", Class: "mass-storage", Serial: "1-0000:00:04.0-1", Vendor: "46f4", Product: "0001"},
	}, delivering(hardware.Delivery{DevNodes: []string{"/dev/sda"}, Blocks: []string{"sda"}}), nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	d := devices[0]
	if d.Name != "usb-2-1-1-0" {
		t.Errorf("name = %q, want the sanitized bus address", d.Name)
	}
	attrs := map[string]string{}
	for name, attr := range d.Attributes {
		if attr.String != nil {
			attrs[name] = *attr.String
		}
	}
	want := map[string]string{
		"bus":      "usb",
		"driver":   "uas",
		"class":    "mass-storage",
		"name":     "QEMU QEMU USB HARDDRIVE",
		"modalias": "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
		"serial":   "1-0000:00:04.0-1",
		"vendor":   "46f4",
		"product":  "0001",
	}
	for name, value := range want {
		if attrs[name] != value {
			t.Errorf("attribute %s = %q, want %q", name, attrs[name], value)
		}
	}
}

func TestInventorySkipsUndrivenDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "", Modalias: "pci:v...", Name: "QEMU Standard VGA"},
	}, delivering(hardware.Delivery{DevNodes: []string{"/dev/dri/card0"}}), nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: undriven hardware is the unclaimed report's story", devices)
	}
}

func TestInventorySkipsBusPlumbing(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1", Driver: "usb", Name: "QEMU QEMU USB HARDDRIVE"},
		{Bus: "usb", Address: "usb2", Driver: "usb", Name: "Linux Foundation xHCI Host Controller"},
		{Bus: "usb", Address: "2-0:1.0", Driver: "hub"},
		{Bus: "pci", Address: "0000:00:01.0", Driver: "pcieport", Name: "Root Port"},
	}, delivering(hardware.Delivery{DevNodes: []string{"/dev/bus/usb/002/001"}}), nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: plumbing is not claimable inventory", devices)
	}
}

func TestInventorySkipsUndeliverableDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:04.0", Driver: "virtio-pci",
			Name: "Red Hat, Inc. Virtio network device", Class: "network"},
	}, delivering(hardware.Delivery{}), nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: a device with no nodes has nothing to hand a pod", devices)
	}
}

func TestInventoryWithholdsThePlatformsOwnDisks(t *testing.T) {
	platform := map[string]bool{"vda1": true}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:06.0", Driver: "virtio-pci",
			Name: "Red Hat, Inc. Virtio block device", Class: "storage"},
	}, delivering(hardware.Delivery{
		DevNodes: []string{"/dev/vda", "/dev/vda1"},
		Blocks:   []string{"vda", "vda1"},
	}), platform)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: the machine stands on this disk", devices)
	}
}

func TestInventoryPublishesAnUnroledDisk(t *testing.T) {
	platform := map[string]bool{"vda1": true}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1:1.0", Driver: "usb-storage", Class: "mass-storage",
			Name: "QEMU QEMU USB HARDDRIVE"},
	}, delivering(hardware.Delivery{
		DevNodes: []string{"/dev/sda"},
		Blocks:   []string{"sda"},
	}), platform)

	if len(devices) != 1 {
		t.Errorf("devices = %+v, want the stick: no role stands on it", devices)
	}
}

func TestInventoryOmitsAttributesTheHardwareLacks(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:09.0", Driver: "virtio-pci",
			Modalias: "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
			Name:     "Red Hat, Inc. Virtio GPU", Class: "display", Vendor: "1af4", Product: "1050"},
	}, delivering(hardware.Delivery{DevNodes: []string{"/dev/dri/renderD128"}}), nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	if _, ok := devices[0].Attributes["serial"]; ok {
		t.Error("a device with no serial must not carry an empty serial attribute")
	}
}

func TestInventoryNamesAreValidDNSLabels(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1F.3", Driver: "snd_hda_intel", Name: "audio"},
		{Bus: "usb", Address: "2-1.4:1.0", Driver: "cdc_acm", Name: "modem"},
	}, delivering(hardware.Delivery{DevNodes: []string{"/dev/snd/pcmC0D0p"}}), nil)

	if devices[0].Name != "pci-0000-00-1f-3" {
		t.Errorf("name = %q, want lowercased with separators dashed", devices[0].Name)
	}
	if devices[1].Name != "usb-2-1-4-1-0" {
		t.Errorf("name = %q, want lowercased with separators dashed", devices[1].Name)
	}
}
