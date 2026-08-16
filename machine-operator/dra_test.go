package main

// The DRA inventory mapping: which discovered devices become
// slice devices, what they are named, and which attributes they
// carry. The publish rule has three tests: driven and not part of
// the bus structure, deliverable, and not the platform's own disk.
// Each test has a case that refuses its counterexample.

import (
	"maps"
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
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/sda", Subsystem: "block", Block: "sda"}}}), nil)

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
		"address":  "2-1:1.0",
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

// gpu is the testbed's integrated GPU: a driven display device whose
// subtree delivers its two DRM nodes, the legacy framebuffer that
// fbdev emulation creates beside them, and the i2c monitor-control
// buses i915 registers for its display outputs. The lab guest showed
// the framebuffer, and the testbed showed the i2c buses; each one
// disproved a narrower rule.
func gpu() ([]hardware.Device, func(hardware.Device) hardware.Delivery) {
	return []hardware.Device{{
			Bus: "pci", Address: "0000:00:02.0", Driver: "i915", Class: "display",
			ClassCode: "030000", Name: "Alder Lake-N [UHD Graphics]", Vendor: "8086", Product: "46d2",
		}}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
			{Path: "/dev/dri/card0", Subsystem: "drm"},
			{Path: "/dev/dri/renderD128", Subsystem: "drm"},
			{Path: "/dev/fb0", Subsystem: "graphics"},
			{Path: "/dev/i2c-0", Subsystem: "i2c-dev"},
			{Path: "/dev/i2c-1", Subsystem: "i2c-dev"},
		}})
}

func TestInventorySplitsTheGPUAndSharesTheGraphicsHalf(t *testing.T) {
	discovered, inspect := gpu()
	devices := inventoryDevices(discovered, inspect, nil)

	if len(devices) != 3 {
		t.Fatalf("devices = %+v, want the graphics device, the display companion, and the i2c companion", devices)
	}
	graphics, display, i2c := devices[0], devices[1], devices[2]

	if display.Name != "pci-0000-00-02-0-display" {
		t.Errorf("name = %q, want the bare name plus the display suffix", display.Name)
	}
	if display.AllowMultipleAllocations != nil {
		t.Error("one card node carries modesetting authority for one process")
	}
	if display.Attributes["displayNode"].Bool == nil || !*display.Attributes["displayNode"].Bool {
		t.Error("a display node is the fact a player selects on")
	}
	if _, ok := display.Attributes["renderNode"]; ok {
		t.Error("the display companion delivers no render node")
	}
	if _, ok := graphics.Attributes["displayNode"]; ok {
		t.Error("the graphics device delivers no card node")
	}

	if graphics.Name != "pci-0000-00-02-0" {
		t.Errorf("name = %q, want the bare name, so an existing allocation stays valid", graphics.Name)
	}
	if graphics.AllowMultipleAllocations == nil || !*graphics.AllowMultipleAllocations {
		t.Error("a device that delivers only DRM nodes may be allocated more than once")
	}
	if graphics.Attributes["renderNode"].Bool == nil || !*graphics.Attributes["renderNode"].Bool {
		t.Error("a render node is the fact a transcoding workload selects on")
	}
	if got := graphics.Attributes["subsystem"].String; got == nil || *got != "drm" {
		t.Errorf("subsystem = %v, want drm now that the delivery is one kind", got)
	}

	if i2c.Name != "pci-0000-00-02-0-i2c-dev" {
		t.Errorf("name = %q, want the bare name plus the subsystem", i2c.Name)
	}
	if i2c.AllowMultipleAllocations != nil {
		t.Error("two raw writers on one i2c wire have no arbitration contract")
	}
	if _, ok := i2c.Attributes["renderNode"]; ok {
		t.Error("the i2c companion has no render node")
	}
	if got := i2c.Attributes["subsystem"].String; got == nil || *got != "i2c-dev" {
		t.Errorf("subsystem = %v", got)
	}
	if got := i2c.Attributes["driver"].String; got == nil || *got != "i915" {
		t.Errorf("driver = %v, want the parent's identifying attributes", got)
	}
}

func TestInventoryGivesOneCardsDevicesOneAddress(t *testing.T) {
	// Two GPUs in one machine. A claim that asks for a render node and
	// a card node pairs its requests with a matchAttribute constraint,
	// and a constraint reads an attribute, never a name. Every device
	// published from one card carries that card's address, and the
	// second card carries its own.
	discovered, inspect := gpu()
	discovered = append(discovered, hardware.Device{
		Bus: "pci", Address: "0000:03:00.0", Driver: "amdgpu", Class: "display",
		ClassCode: "030000", Name: "Navi 23 [Radeon RX 6600]", Vendor: "1002", Product: "73ff",
	})
	devices := inventoryDevices(discovered, inspect, nil)

	addresses := map[string]string{}
	for _, d := range devices {
		if got := d.Attributes["address"].String; got != nil {
			addresses[d.Name] = *got
		}
	}
	want := map[string]string{
		"pci-0000-00-02-0":         "0000:00:02.0",
		"pci-0000-00-02-0-display": "0000:00:02.0",
		"pci-0000-00-02-0-i2c-dev": "0000:00:02.0",
		"pci-0000-03-00-0":         "0000:03:00.0",
		"pci-0000-03-00-0-display": "0000:03:00.0",
		"pci-0000-03-00-0-i2c-dev": "0000:03:00.0",
	}
	if !maps.Equal(addresses, want) {
		t.Errorf("addresses = %v, want every device of a card to carry that card's own address", addresses)
	}
}

func TestInventoryPublishesAnAudioControllerExclusively(t *testing.T) {
	// The HDA controller on the testbed, with the nodes its sysfs
	// subtree holds: the card's own nodes, and the input device that
	// ALSA registers for each HDMI jack the codec can sense. The card
	// is exclusive because one sound server owns every PCM on it, and
	// a second claimant should wait in the scheduler rather than meet
	// EBUSY at play time.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1f.3", Driver: "snd_hda_intel", Class: "multimedia",
			Name: "Alder Lake-N PCH High Definition Audio"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/controlC0", Subsystem: "sound"},
		{Path: "/dev/snd/pcmC0D3p", Subsystem: "sound"},
		{Path: "/dev/input/event16", Subsystem: "input"},
	}}), nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the controller", devices)
	}
	if devices[0].AllowMultipleAllocations != nil && *devices[0].AllowMultipleAllocations {
		t.Error("an audio controller belongs to one claim")
	}
	if got := devices[0].Attributes["subsystem"].String; got == nil || *got != "sound" {
		t.Errorf("subsystem = %v, want sound", got)
	}
	if _, ok := devices[0].Attributes["renderNode"]; ok {
		t.Error("an audio controller has no render node")
	}
}

func TestInventoryRefusesToShareARenderNodeBesideSomethingElse(t *testing.T) {
	// A render node beside a node from outside the graphics stack is
	// hardware liken has not met. It stays exclusive until somebody
	// looks at it.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "novel", Class: "display"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/ttyS4", Subsystem: "tty"},
	}}), nil)

	if devices[0].AllowMultipleAllocations != nil {
		t.Error("only a whole graphics device may be shared")
	}
}

func TestInventoryKeepsEveryOtherDeviceExclusive(t *testing.T) {
	// A USB serial adapter. One process opens the port, so the API
	// must allocate it once, and the device says nothing rather than
	// claiming to be exclusive.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "3-1:1.0", Driver: "cp210x", Class: "vendor-specific", ClassCode: "ff"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyUSB0", Subsystem: "tty"},
	}}), nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	if devices[0].AllowMultipleAllocations != nil {
		t.Error("a device that does not divide publishes nothing about sharing")
	}
	if _, ok := devices[0].Attributes["renderNode"]; ok {
		t.Error("a serial port has no render node")
	}
}

func TestInventoryNamesNoSubsystemForAMixedDelivery(t *testing.T) {
	// A device that hands over two kinds of node at once has no
	// single answer, and it is not shareable on the strength of one
	// of them.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1f.3", Driver: "snd_hda_intel", Class: "multimedia"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/pcmC0D0p", Subsystem: "sound"},
		{Path: "/dev/dri/renderD129", Subsystem: "drm"},
	}}), nil)

	if _, ok := devices[0].Attributes["subsystem"]; ok {
		t.Error("a mixed delivery names no one subsystem")
	}
	if devices[0].AllowMultipleAllocations != nil {
		t.Error("only a delivery that is entirely DRM may be shared")
	}
}

func TestInventorySkipsUndrivenDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "", Modalias: "pci:v...", Name: "QEMU Standard VGA"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/dri/card0", Subsystem: "drm"}}}), nil)

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
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/bus/usb/002/001", Subsystem: "usb"}}}), nil)

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

func TestInventoryPublishesAnIdleBluetoothAdapter(t *testing.T) {
	// An adapter with nothing connected to it delivers no node from its
	// subtree, because hci is a socket interface and the radio has no
	// node. The device is still real hardware a workload claims, so the
	// driver is what puts it in the slice, and the usbfs node is what
	// the claim delivers.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "1-8:1.0", Driver: "btusb", Class: "wireless", ClassCode: "e0",
			Name: "Intel AX211 Bluetooth", Vendor: "8087", Product: "0033"},
	}, delivering(hardware.Delivery{BusNode: "/dev/bus/usb/001/008"}), nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the adapter", devices)
	}
	adapter := devices[0]
	if adapter.Name != "usb-1-8-1-0" {
		t.Errorf("name = %q, want the sanitized bus address", adapter.Name)
	}
	if adapter.AllowMultipleAllocations != nil {
		t.Error("one Bluetooth stack drives one radio")
	}
	if got := adapter.Attributes["driver"].String; got == nil || *got != "btusb" {
		t.Errorf("driver = %v, want btusb, the attribute a DeviceClass selects an adapter by", got)
	}
	if _, ok := adapter.Attributes["subsystem"]; ok {
		t.Error("the usbfs node is not a kind of node the policy sorts by")
	}
}

func TestInventoryWithholdsThePlatformsOwnDisks(t *testing.T) {
	platform := map[string]bool{"vda1": true}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:06.0", Driver: "virtio-pci",
			Name: "Red Hat, Inc. Virtio block device", Class: "storage"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/vda", Subsystem: "block", Block: "vda"},
		{Path: "/dev/vda1", Subsystem: "block", Block: "vda1"},
	}}), platform)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none: a platform role holds this disk", devices)
	}
}

func TestInventoryPublishesAnUnroledDisk(t *testing.T) {
	platform := map[string]bool{"vda1": true}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1:1.0", Driver: "usb-storage", Class: "mass-storage",
			Name: "QEMU QEMU USB HARDDRIVE"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/sda", Subsystem: "block", Block: "sda"},
	}}), platform)

	if len(devices) != 1 {
		t.Errorf("devices = %+v, want the stick: no platform role holds it", devices)
	}
}

func TestInventoryOmitsAttributesTheHardwareLacks(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:09.0", Driver: "virtio-pci",
			Modalias: "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
			Name:     "Red Hat, Inc. Virtio GPU", Class: "display", Vendor: "1af4", Product: "1050"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/dri/renderD128", Subsystem: "drm"}}}), nil)

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
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/snd/pcmC0D0p", Subsystem: "sound"}}}), nil)

	if devices[0].Name != "pci-0000-00-1f-3" {
		t.Errorf("name = %q, want lowercased with separators dashed", devices[0].Name)
	}
	if devices[1].Name != "usb-2-1-4-1-0" {
		t.Errorf("name = %q, want lowercased with separators dashed", devices[1].Name)
	}
}
