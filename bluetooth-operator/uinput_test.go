package main

// These tests hold the kernel interface to two promises. The first is
// arithmetic: an ioctl request number this file computes must equal the
// number linux/input.h and linux/uinput.h define, because a wrong
// number reaches a different driver call or none. The second needs a
// kernel: the real-kernel tests create a uinput device, read back the
// node the kernel gave it, and hold the kernel to what a mask on that
// node filters. They skip where /dev/uinput is not openable, which is
// CI.

import (
	"encoding/binary"
	"os"
	"slices"
	"testing"
	"time"
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
		{name: "EVIOCSMASK", request: eviocsmask, want: 0x40104593},
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

// rawEvent is one struct input_event as the kernel lays it out: two
// timestamp words this program leaves at zero, then the type, the
// code, and the value.
func rawEvent(eventType, code uint16, value int32) []byte {
	record := make([]byte, inputEventSize)
	binary.NativeEndian.PutUint16(record[16:], eventType)
	binary.NativeEndian.PutUint16(record[18:], code)
	binary.NativeEndian.PutUint32(record[20:], uint32(value))
	return record
}

// deliveredEventTypes reads one batch from a real node and answers
// with the type of each record in it. A read that never returns is a
// mask that filtered more than the test asked it to, so the wait is
// bounded and a timeout fails the test.
func deliveredEventTypes(t *testing.T, node realNode) []uint16 {
	t.Helper()
	records := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, inputEventSize*64)
		read, err := node.Read(buffer)
		if err != nil {
			records <- nil
			return
		}
		records <- buffer[:read]
	}()
	select {
	case got := <-records:
		var types []uint16
		for at := 0; at+inputEventSize <= len(got); at += inputEventSize {
			types = append(types, binary.NativeEndian.Uint16(got[at+16:]))
		}
		return types
	case <-time.After(2 * time.Second):
		t.Fatal("the node delivered nothing within two seconds")
		return nil
	}
}

// The kernel filters what it queues on a node this operator narrowed.
// This is the whole mechanism by which a class no claim asked for
// costs nothing: the events are dropped before they reach the fd, so
// the pump never wakes for them.
func TestNarrowARealNodeOnTheRealKernel(t *testing.T) {
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
	node, err := linuxInput{}.open(device.node())
	if err != nil {
		// The operator opens a controller's node as root, and a
		// workstation user is not in the group that owns one.
		t.Skipf("this machine does not permit reading %s: %v", device.node(), err)
	}
	t.Cleanup(func() { _ = node.Close() })

	// With no mask the node delivers every type the device declares.
	frame := append(rawEvent(evAbs, 0x00, 100), rawEvent(evSyn, 0, 0)...)
	if err := device.write(frame); err != nil {
		t.Fatal(err)
	}
	if types := deliveredEventTypes(t, node); !slices.Contains(types, uint16(evAbs)) {
		t.Fatalf("the unmasked node delivered types %v, want an absolute event", types)
	}

	if err := node.narrow([]eventMask{{event: evSyn, max: eventTypeMax, codes: []uint16{evKey}}}); err != nil {
		t.Fatal(err)
	}

	// The absolute frame is dropped whole, because the kernel drops the
	// frame marker of a frame it emptied.
	frame = append(rawEvent(evAbs, 0x00, 200), rawEvent(evSyn, 0, 0)...)
	frame = append(frame, rawEvent(evKey, 0x130, 1)...)
	frame = append(frame, rawEvent(evSyn, 0, 0)...)
	if err := device.write(frame); err != nil {
		t.Fatal(err)
	}
	types := deliveredEventTypes(t, node)
	if slices.Contains(types, uint16(evAbs)) {
		t.Errorf("the narrowed node delivered types %v, want no absolute event", types)
	}
	if !slices.Contains(types, uint16(evKey)) {
		t.Errorf("the narrowed node delivered types %v, want the key event", types)
	}
}
