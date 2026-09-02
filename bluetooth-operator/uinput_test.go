package main

// These tests hold the kernel interface to two promises. The first is
// arithmetic: an ioctl request number this file computes must equal the
// number linux/input.h and linux/uinput.h define, because a wrong
// number reaches a different driver call or none. The second needs a
// kernel: the real-kernel test creates a uinput device and reads back
// the node the kernel gave it. It skips where /dev/uinput is not
// openable, which is CI.

import (
	"os"
	"testing"
)

// The request numbers as the kernel's headers define them. Each one is
// the value a C program compiled against linux/input.h or
// linux/uinput.h sends.
func TestIoctlRequestNumbersMatchTheKernelHeaders(t *testing.T) {
	cases := []struct {
		name    string
		request uint32
		want    uint32
	}{
		{name: "EVIOCGID", request: eviocgid, want: 0x80084502},
		{name: "EVIOCGNAME(80)", request: eviocgname(80), want: 0x80504506},
		{name: "EVIOCGPROP(4)", request: eviocgprop(4), want: 0x80044509},
		{name: "EVIOCGBIT(0, 4)", request: eviocgbit(0, 4), want: 0x80044520},
		{name: "EVIOCGABS(ABS_X)", request: eviocgabs(0), want: 0x80184540},
		{name: "UI_DEV_CREATE", request: uiDevCreate, want: 0x5501},
		{name: "UI_DEV_DESTROY", request: uiDevDestroy, want: 0x5502},
		{name: "UI_DEV_SETUP", request: uiDevSetup, want: 0x405c5503},
		{name: "UI_ABS_SETUP", request: uiAbsSetup, want: 0x401c5504},
		{name: "UI_SET_EVBIT", request: uiSetEvBit, want: 0x40045564},
		{name: "UI_SET_KEYBIT", request: uiSetKeyBit, want: 0x40045565},
		{name: "UI_SET_ABSBIT", request: uiSetAbsBit, want: 0x40045567},
		{name: "UI_SET_PHYS", request: uiSetPhys, want: 0x4008556c},
		{name: "UI_SET_PROPBIT", request: uiSetPropBit, want: 0x4004556e},
		{name: "UI_GET_SYSNAME(80)", request: uiGetSysname(80), want: 0x8050552c},
	}
	for _, c := range cases {
		if c.request != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.request, c.want)
		}
	}
}

// One struct input_event is 24 bytes on a 64-bit kernel, and the relay
// truncates a partial read to a multiple of that.
func TestOneEventRecordIsTwentyFourBytes(t *testing.T) {
	if inputEventSize != 24 {
		t.Errorf("inputEventSize = %d, want 24", inputEventSize)
	}
}

func TestWithinDeliveredRange(t *testing.T) {
	cases := []struct {
		node string
		want bool
	}{
		{node: "/dev/input/event0", want: true},
		{node: "/dev/input/event31", want: true},
		{node: "/dev/input/event32", want: false},
		{node: "/dev/input/event140", want: false},
		{node: "/dev/input/js0", want: false},
	}
	for _, c := range cases {
		if got := withinDeliveredRange(c.node); got != c.want {
			t.Errorf("withinDeliveredRange(%q) = %v, want %v", c.node, got, c.want)
		}
	}
}

// testCapabilities is a small gamepad: one button, one stick axis, and
// a name the relay matches a real node to its virtual device by.
func testCapabilities() evdevCapabilities {
	return evdevCapabilities{
		Name: "Wireless Controller",
		ID:   evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6, Version: 0x8111},
		Codes: map[string][]uint16{
			"EV_KEY": {0x130},
			"EV_ABS": {0x00},
		},
		Axes: []absAxis{{Code: 0x00, Minimum: 0, Maximum: 255, Flat: 15}},
	}
}

// The kernel itself creates the device, gives it a node, and takes the
// node away when the fd closes.
func TestCreateAVirtualDeviceOnTheRealKernel(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(testCapabilities(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatal(err)
	}
	node := device.node()
	if _, err := os.Stat(node); err != nil {
		t.Fatalf("the kernel reported node %s: %v", node, err)
	}
	// The relay moves whole records and reads none of them, so a write
	// of one record is the whole contract with the kernel.
	if err := device.write(make([]byte, inputEventSize)); err != nil {
		t.Errorf("writing one event: %v", err)
	}

	if err := device.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(node); !os.IsNotExist(err) {
		t.Errorf("%s is still there after the fd closed: %v", node, err)
	}
}

// The capabilities the kernel reports for a device this test created
// are the capabilities it was created with. This is the round trip the
// relay makes on every reconnect: read a real node, build a virtual
// one that matches it.
func TestReadBackTheCapabilitiesOfADeviceThisTestCreated(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(testCapabilities(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = device.close() })

	caps, err := linuxInput{}.readCapabilities(device.node())
	if err != nil {
		t.Skipf("this machine does not permit reading %s: %v", device.node(), err)
	}
	want := testCapabilities()
	if caps.Name != want.Name || caps.ID != want.ID {
		t.Errorf("name and id = %q %+v, want %q %+v", caps.Name, caps.ID, want.Name, want.ID)
	}
	if len(caps.Codes["EV_KEY"]) != 1 || caps.Codes["EV_KEY"][0] != 0x130 {
		t.Errorf("EV_KEY codes = %v", caps.Codes["EV_KEY"])
	}
	if len(caps.Axes) != 1 || caps.Axes[0] != want.Axes[0] {
		t.Errorf("axes = %+v, want %+v", caps.Axes, want.Axes)
	}
}
