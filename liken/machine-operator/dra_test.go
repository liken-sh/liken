package main

// Tests of which discovered devices become ResourceSlice devices, and
// of their names and attributes.

import (
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/liken/hardware"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// delivering returns an inspect function that gives every device the
// same delivery, in place of a sysfs walk.
func delivering(d hardware.Delivery) func(hardware.Device) hardware.Delivery {
	return func(hardware.Device) hardware.Delivery { return d }
}

// noRoles is the protection of a machine with known facts and no
// disk-backed role, so it withholds nothing.
var noRoles = protection{known: true}

func TestInventoryPublishesDrivenDeliverableDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1:1.0", Driver: "uas", Modalias: "usb:v46F4p0001d0100dc00dsc00dp00ic08isc06ip50in00",
			Name: "QEMU QEMU USB HARDDRIVE", Class: "mass-storage", Serial: "1-0000:00:04.0-1", Vendor: "46f4", Product: "0001"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/sda", Subsystem: "block", Block: "sda"}}}), noRoles, nil)

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

// A USB device publishes its bus number and device number, which
// firmware-operator uses to build the usbfs path and to tell
// enumerations of a device apart. A PCI device has neither.
func TestInventoryPublishesTheUSBBusAndDeviceNumbers(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "1-4.1.2", Driver: "usb", Vendor: "1618", Product: "0920", BusNumber: 1, DeviceNumber: 12},
		{Bus: "usb", Address: "1-4.1.2:1.0", Class: "vendor-specific"},
		{Bus: "pci", Address: "0000:00:02.0", Driver: "i915", Class: "display"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/bus/usb/001/012", Subsystem: "usb"}}}), noRoles, nil)

	numbers := map[string][2]*int64{}
	for _, d := range devices {
		numbers[d.Name] = [2]*int64{d.Attributes["busNumber"].Int, d.Attributes["deviceNumber"].Int}
	}
	usb := numbers["usb-1-4-1-2"]
	if usb[0] == nil || *usb[0] != 1 || usb[1] == nil || *usb[1] != 12 {
		t.Errorf("usb-1-4-1-2 numbers = %v, want bus 1 and device 12", usb)
	}
	if pci := numbers["pci-0000-00-02-0"]; pci[0] != nil || pci[1] != nil {
		t.Errorf("pci-0000-00-02-0 numbers = %v, want none", pci)
	}
}

// gpu returns an integrated GPU with the nodes that i915 creates: the
// two DRM nodes, the framebuffer that fbdev emulation adds, and the
// i2c buses for monitor control.
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
	devices := inventoryDevices(discovered, inspect, noRoles, nil)

	if len(devices) != 3 {
		t.Fatalf("devices = %+v, want the graphics device, the display companion, and the i2c companion", devices)
	}
	graphics, display, i2c := devices[0], devices[1], devices[2]

	if display.Name != "pci-0000-00-02-0-display" {
		t.Errorf("name = %q, want the bare name plus the display suffix", display.Name)
	}
	if display.AllowMultipleAllocations != nil {
		t.Error("the display device allows multiple allocations, want exclusive because one process holds the card node")
	}
	if display.Attributes["displayNode"].Bool == nil || !*display.Attributes["displayNode"].Bool {
		t.Error("the display device has no true displayNode attribute, want one for a Player to select on")
	}
	if _, ok := display.Attributes["renderNode"]; ok {
		t.Error("the display device has a renderNode attribute, want none")
	}
	if _, ok := graphics.Attributes["displayNode"]; ok {
		t.Error("the graphics device has a displayNode attribute, want none")
	}

	if graphics.Name != "pci-0000-00-02-0" {
		t.Errorf("name = %q, want the bare name, so an existing allocation stays valid", graphics.Name)
	}
	if graphics.AllowMultipleAllocations == nil || !*graphics.AllowMultipleAllocations {
		t.Error("the graphics device does not allow multiple allocations, want shared because it delivers only DRM nodes")
	}
	if graphics.Attributes["renderNode"].Bool == nil || !*graphics.Attributes["renderNode"].Bool {
		t.Error("the graphics device has no true renderNode attribute, want one for a transcoding workload to select on")
	}
	if got := graphics.Attributes["subsystem"].String; got == nil || *got != "drm" {
		t.Errorf("subsystem = %v, want drm because every node of the graphics device is DRM", got)
	}

	if i2c.Name != "pci-0000-00-02-0-i2c-dev" {
		t.Errorf("name = %q, want the bare name plus the subsystem", i2c.Name)
	}
	if i2c.AllowMultipleAllocations != nil {
		t.Error("the i2c device allows multiple allocations, want exclusive because two writers on one i2c bus have no arbitration")
	}
	if _, ok := i2c.Attributes["renderNode"]; ok {
		t.Error("the i2c device has a renderNode attribute, want none")
	}
	if got := i2c.Attributes["subsystem"].String; got == nil || *got != "i2c-dev" {
		t.Errorf("subsystem = %v, want i2c-dev", got)
	}
	if got := i2c.Attributes["driver"].String; got == nil || *got != "i915" {
		t.Errorf("driver = %v, want i915, the parent device's driver", got)
	}
}

func TestInventoryGivesOneCardsDevicesOneAddress(t *testing.T) {
	discovered, inspect := gpu()
	discovered = append(discovered, hardware.Device{
		Bus: "pci", Address: "0000:03:00.0", Driver: "amdgpu", Class: "display",
		ClassCode: "030000", Name: "Navi 23 [Radeon RX 6600]", Vendor: "1002", Product: "73ff",
	})
	devices := inventoryDevices(discovered, inspect, noRoles, nil)

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
		t.Errorf("addresses = %v, want each device to have its own card's address", addresses)
	}
}

func TestInventoryStampsThePCIBusIDOnEveryDeviceOfAPCICard(t *testing.T) {
	discovered, inspect := gpu()
	discovered = append(discovered, hardware.Device{
		Bus: "usb", Address: "2-1:1.0", Driver: "uas", Class: "mass-storage",
	})
	devices := inventoryDevices(discovered, inspect, noRoles, nil)

	busIDs := map[string]string{}
	for _, d := range devices {
		if got := d.Attributes[pciBusIDAttribute].String; got != nil {
			busIDs[d.Name] = *got
		}
	}
	want := map[string]string{
		"pci-0000-00-02-0":         "0000:00:02.0",
		"pci-0000-00-02-0-display": "0000:00:02.0",
		"pci-0000-00-02-0-i2c-dev": "0000:00:02.0",
	}
	if !maps.Equal(busIDs, want) {
		t.Errorf("%s = %v, want the card's address on each of its devices and none on the USB disk",
			pciBusIDAttribute, busIDs)
	}
}

func TestInventoryPublishesAnAudioControllerExclusively(t *testing.T) {
	// An HDA controller has the card's nodes and an input device that
	// ALSA registers for each HDMI jack. One sound server opens every
	// PCM of the card, so the card goes to one claim. A second claim
	// then stays pending in the scheduler instead of failing with EBUSY
	// when it plays.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1f.3", Driver: "snd_hda_intel", Class: "multimedia",
			Name: "Alder Lake-N PCH High Definition Audio"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/controlC0", Subsystem: "sound"},
		{Path: "/dev/snd/pcmC0D3p", Subsystem: "sound"},
		{Path: "/dev/input/event16", Subsystem: "input"},
	}}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the controller", devices)
	}
	if devices[0].AllowMultipleAllocations != nil && *devices[0].AllowMultipleAllocations {
		t.Error("the audio controller allows multiple allocations, want exclusive")
	}
	if got := devices[0].Attributes["subsystem"].String; got == nil || *got != "sound" {
		t.Errorf("subsystem = %v, want sound", got)
	}
	if _, ok := devices[0].Attributes["renderNode"]; ok {
		t.Error("the audio controller has a renderNode attribute, want none")
	}
}

func TestInventoryStampsSupportsSoundOnASoundDevice(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1f.3", Driver: "snd_hda_intel", Class: "multimedia",
			Name: "Alder Lake-N PCH High Definition Audio"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/controlC0", Subsystem: "sound"},
		{Path: "/dev/snd/pcmC0D3p", Subsystem: "sound"},
	}}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the controller", devices)
	}
	got := devices[0].Attributes["sound.liken.sh/supportsSound"].Bool
	if got == nil || !*got {
		t.Errorf("sound.liken.sh/supportsSound = %v, want true", got)
	}
}

func TestInventoryStampsSupportsSoundOnNothingElse(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "i915", Class: "display"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/card0", Subsystem: "drm"},
	}}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the card device", devices)
	}
	if _, ok := devices[0].Attributes["sound.liken.sh/supportsSound"]; ok {
		t.Error("the display device has a supportsSound attribute, want none")
	}
}

func TestInventoryRefusesToShareARenderNodeBesideSomethingElse(t *testing.T) {
	// A render node beside a node from outside the graphics stack is
	// an unknown kind of device, so it stays exclusive.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "novel", Class: "display"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/dri/renderD128", Subsystem: "drm"},
		{Path: "/dev/ttyS4", Subsystem: "tty"},
	}}), noRoles, nil)

	if devices[0].AllowMultipleAllocations != nil {
		t.Error("the device allows multiple allocations, want exclusive because it has a node outside the graphics stack")
	}
}

func TestInventoryKeepsEveryOtherDeviceExclusive(t *testing.T) {
	// One process opens a serial port, so the device goes to one claim.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "3-1:1.0", Driver: "cp210x", Class: "vendor-specific", ClassCode: "ff"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/ttyUSB0", Subsystem: "tty"},
	}}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	if devices[0].AllowMultipleAllocations != nil {
		t.Error("the serial device allows multiple allocations, want exclusive")
	}
	if _, ok := devices[0].Attributes["renderNode"]; ok {
		t.Error("the serial device has a renderNode attribute, want none")
	}
}

func TestInventoryNamesNoSubsystemForAMixedDelivery(t *testing.T) {
	// A delivery of two kinds of node has no single subsystem, and
	// one kind alone does not make the device shareable.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1f.3", Driver: "snd_hda_intel", Class: "multimedia"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/snd/pcmC0D0p", Subsystem: "sound"},
		{Path: "/dev/dri/renderD129", Subsystem: "drm"},
	}}), noRoles, nil)

	if _, ok := devices[0].Attributes["subsystem"]; ok {
		t.Error("the device has a subsystem attribute, want none because its nodes are of two subsystems")
	}
	if devices[0].AllowMultipleAllocations != nil {
		t.Error("the device allows multiple allocations, want exclusive because only part of its delivery is DRM")
	}
}

func TestInventorySkipsUndrivenDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:02.0", Driver: "", Modalias: "pci:v...", Name: "QEMU Standard VGA"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/dri/card0", Subsystem: "drm"}}}), noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none because no driver is bound", devices)
	}
}

func TestInventorySkipsUSBDevicesHubsAndPorts(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1", Driver: "usb", Name: "QEMU QEMU USB HARDDRIVE"},
		{Bus: "usb", Address: "usb2", Driver: "usb", Name: "Linux Foundation xHCI Host Controller"},
		{Bus: "usb", Address: "2-0:1.0", Driver: "hub"},
		{Bus: "pci", Address: "0000:00:01.0", Driver: "pcieport", Name: "Root Port"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/bus/usb/002/001", Subsystem: "usb"}}}), noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none because hubs, host controllers, and ports are not published", devices)
	}
}

func TestInventorySkipsUndeliverableDevices(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:04.0", Driver: "virtio-pci",
			Name: "Red Hat, Inc. Virtio network device", Class: "network"},
	}, delivering(hardware.Delivery{}), noRoles, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none because the device has no nodes", devices)
	}
}

func TestInventoryPublishesAnIdleBluetoothAdapter(t *testing.T) {
	// An adapter has no device node, because hci is a socket
	// interface. The btusb driver alone puts it in the slice, and a
	// claim receives its usbfs node.
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "1-8:1.0", Driver: "btusb", Class: "wireless", ClassCode: "e0",
			Name: "Intel AX211 Bluetooth", Vendor: "8087", Product: "0033"},
	}, delivering(hardware.Delivery{BusNode: "/dev/bus/usb/001/008"}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want the adapter", devices)
	}
	adapter := devices[0]
	if adapter.Name != "usb-1-8-1-0" {
		t.Errorf("name = %q, want the sanitized bus address", adapter.Name)
	}
	if adapter.AllowMultipleAllocations != nil {
		t.Error("the adapter allows multiple allocations, want exclusive")
	}
	if got := adapter.Attributes["driver"].String; got == nil || *got != "btusb" {
		t.Errorf("driver = %v, want btusb, the attribute a DeviceClass selects an adapter by", got)
	}
	if _, ok := adapter.Attributes["subsystem"]; ok {
		t.Error("the adapter has a subsystem attribute, want none because its only node is the usbfs node")
	}
}

func TestInventoryWithholdsThePlatformsOwnDisks(t *testing.T) {
	platform := protection{known: true, blocks: map[string]bool{"vda1": true}}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:06.0", Driver: "virtio-pci",
			Name: "Red Hat, Inc. Virtio block device", Class: "storage"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/vda", Subsystem: "block", Block: "vda"},
		{Path: "/dev/vda1", Subsystem: "block", Block: "vda1"},
	}}), platform, nil)

	if len(devices) != 0 {
		t.Errorf("devices = %+v, want none because a platform role holds the disk", devices)
	}
}

func TestInventoryPublishesAnUnroledDisk(t *testing.T) {
	platform := protection{known: true, blocks: map[string]bool{"vda1": true}}
	devices := inventoryDevices([]hardware.Device{
		{Bus: "usb", Address: "2-1:1.0", Driver: "usb-storage", Class: "mass-storage",
			Name: "QEMU QEMU USB HARDDRIVE"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/sda", Subsystem: "block", Block: "sda"},
	}}), platform, nil)

	if len(devices) != 1 {
		t.Errorf("devices = %+v, want the stick because no platform role holds it", devices)
	}
}

func TestInventoryOmitsAttributesTheHardwareLacks(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:09.0", Driver: "virtio-pci",
			Modalias: "pci:v00001AF4d00001050sv00001AF4sd00001100bc03sc80i00",
			Name:     "Red Hat, Inc. Virtio GPU", Class: "display", Vendor: "1af4", Product: "1050"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/dri/renderD128", Subsystem: "drm"}}}), noRoles, nil)

	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want 1", devices)
	}
	if _, ok := devices[0].Attributes["serial"]; ok {
		t.Error("the device has a serial attribute, want none because the hardware has no serial")
	}
}

func TestInventoryNamesAreValidDNSLabels(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "pci", Address: "0000:00:1F.3", Driver: "snd_hda_intel", Name: "audio"},
		{Bus: "usb", Address: "2-1.4:1.0", Driver: "cdc_acm", Name: "modem"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{{Path: "/dev/snd/pcmC0D0p", Subsystem: "sound"}}}), noRoles, nil)

	if devices[0].Name != "pci-0000-00-1f-3" {
		t.Errorf("name = %q, want pci-0000-00-1f-3", devices[0].Name)
	}
	if devices[1].Name != "usb-2-1-4-1-0" {
		t.Errorf("name = %q, want usb-2-1-4-1-0", devices[1].Name)
	}
}

// A board device is named by the firmware, with characters that a DNS
// label cannot hold.
func TestInventoryNamesBoardDevicesAsDNSLabels(t *testing.T) {
	cases := []struct {
		device hardware.Device
		want   string
	}{
		{hardware.Device{Bus: "platform", Address: "MSFT0101:00"}, "platform-msft0101-00"},
		{hardware.Device{Bus: "platform", Address: "acpi.video_bus.0"}, "platform-acpi-video-bus-0"},
		{hardware.Device{Bus: "platform", Address: "intel_pmc_core.0"}, "platform-intel-pmc-core-0"},
		{hardware.Device{Bus: "platform", Address: "a-firmware-name-long-enough-to-run-past-the-limit-of-one-label"}, "platform-a-firmware-name-long-enough-to-run-past-the-limit-of-o"},
	}
	for _, tc := range cases {
		t.Run(tc.device.Address, func(t *testing.T) {
			if got := deviceName(tc.device); got != tc.want {
				t.Errorf("deviceName = %q, want %q", got, tc.want)
			}
		})
	}
}

// A TPM on the platform bus publishes like any device with a driver
// and nodes. It is exclusive, because nothing shows that two
// workloads can share a TPM.
func TestInventoryPublishesABoardTPM(t *testing.T) {
	devices := inventoryDevices([]hardware.Device{
		{Bus: "platform", Address: "MSFT0101:00", Driver: "tpm_crb_acpi", Modalias: "acpi:MSFT0101:"},
	}, delivering(hardware.Delivery{Nodes: []hardware.DeliveredNode{
		{Path: "/dev/tpm0", Subsystem: "tpm"},
		{Path: "/dev/tpmrm0", Subsystem: "tpmrm"},
	}}), noRoles, nil)

	if len(devices) != 1 || devices[0].Name != "platform-msft0101-00" {
		t.Fatalf("devices = %+v, want the TPM", devices)
	}
	if got := devices[0].Attributes["driver"].String; got == nil || *got != "tpm_crb_acpi" {
		t.Errorf("driver = %v, want tpm_crb_acpi", got)
	}
	if devices[0].AllowMultipleAllocations != nil {
		t.Error("the TPM allows multiple allocations, want exclusive")
	}
}

// A ResourceSlice holds at most 128 devices. For a machine with more,
// the slice has the first 128 in name order.
func TestTheInventoryDropsTheDevicesPastOneSlice(t *testing.T) {
	isolatePass(t)
	for i := range maxSliceDevices + 1 {
		plugInSoundCardAt(t, draSysfsRoot, fmt.Sprintf("0000:%02x:00.0", i+1), i)
	}
	client, _ := passClients(t, newPassAPI())
	o := metrics.NewOperator(component, machine.Version, []string{machineKind}, watchKinds)
	node := &nodeObject{}
	node.Metadata.Name, node.Metadata.UID = "node-1", "uid-node-1"

	err := publishDeviceInventory(&reader{client: client}, node, nil, nil, newMachineMetrics(o, &fetcher{}))

	slice := &kubernetes.ResourceSlice{}
	if readErr := client.RequestJSON(http.MethodGet, kubernetes.ResourceSlicesPath+"/node-1-liken.sh", nil, slice); err != nil || readErr != nil {
		t.Fatalf("publishing the inventory: %v; reading the ResourceSlice: %v", err, readErr)
	}
	if got := len(slice.Spec.Devices); got != maxSliceDevices || slice.Spec.Devices[0].Name != "pci-0000-01-00-0" {
		t.Errorf("the slice has %d devices starting at %s, want %d starting at pci-0000-01-00-0", got, slice.Spec.Devices[0].Name, maxSliceDevices)
	}
}
