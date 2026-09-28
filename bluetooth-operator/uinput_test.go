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
		// The fuzz and the flat travel with the axis, so a consumer
		// reading the virtual node sees the same smoothing the real one
		// reports.
		Axes: []absAxis{{Code: 0x00, Minimum: 0, Maximum: 255, Fuzz: 3, Flat: 15}},
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
	var types []uint16
	for at, got := 0, deliveredEventRecords(t, node); at+inputEventSize <= len(got); at += inputEventSize {
		types = append(types, binary.NativeEndian.Uint16(got[at+16:]))
	}
	return types
}

// deliveredEventRecords reads one batch from a real node. A read that
// never returns is a mask or an axis that filtered more than the test
// asked it to, so the wait is bounded and a timeout fails the test.
func deliveredEventRecords(t *testing.T, node realNode) []byte {
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
		return got
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

// steadyAxis is one axis with no smoothing at all, which is a stick
// that reports every step of its position noise.
func steadyAxis() evdevCapabilities {
	return evdevCapabilities{
		Name:  "Wireless Controller",
		ID:    evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{"EV_KEY": {0x130}, "EV_ABS": {0x00}},
		Axes:  []absAxis{{Code: 0x00, Minimum: 0, Maximum: 255}},
	}
}

// deliveredAxisValues reads one batch from a real node and answers
// with the value of each absolute event in it.
func deliveredAxisValues(t *testing.T, node realNode) []int32 {
	t.Helper()
	var values []int32
	for at, got := 0, deliveredEventRecords(t, node); at+inputEventSize <= len(got); at += inputEventSize {
		if binary.NativeEndian.Uint16(got[at+16:]) != evAbs {
			continue
		}
		values = append(values, int32(binary.NativeEndian.Uint32(got[at+20:])))
	}
	return values
}

// The kernel drops a position change smaller than half the axis fuzz
// before any handler sees it. This is the whole mechanism by which a
// stick's noise at rest costs nothing: the events never reach the fd,
// so the pump never wakes for them.
func TestTuneARealAxisOnTheRealKernel(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(steadyAxis(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
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

	// The axis settles at 100 with no smoothing, and one step of noise
	// reaches the reader.
	writeFrames(t, device, [][2]int32{{evAbs, 100}, {evAbs, 101}})
	if got := deliveredAxisValues(t, node); !slices.Contains(got, 101) {
		t.Fatalf("the untuned axis delivered %v, want the one-step change", got)
	}

	current, err := node.axisRange(0x00)
	if err != nil {
		t.Fatal(err)
	}
	tuned := tunedAxis(absAxis{Code: 0x00}, current, axisOverride{Fuzz: ptr(int32(4))})
	if err := node.setAxisRange(0x00, tuned); err != nil {
		t.Fatal(err)
	}
	if back, err := node.axisRange(0x00); err != nil || back.Fuzz != 4 {
		t.Fatalf("the axis reports fuzz %d after the write: %v", back.Fuzz, err)
	}

	// One step of noise is now smaller than half the fuzz, so the
	// kernel drops it and drops the frame it emptied. A real move
	// still arrives.
	writeFrames(t, device, [][2]int32{{evAbs, 102}, {evAbs, 150}})
	got := deliveredAxisValues(t, node)
	if slices.Contains(got, 102) {
		t.Errorf("the tuned axis delivered %v, want the one-step change dropped", got)
	}
	if !slices.Contains(got, 150) {
		t.Errorf("the tuned axis delivered %v, want the real move", got)
	}
}

// writeFrames injects one event and a frame marker for each pair, the
// way a device reports a position change.
func writeFrames(t *testing.T, device virtualDevice, frames [][2]int32) {
	t.Helper()
	var records []byte
	for _, frame := range frames {
		records = append(records, rawEvent(uint16(frame[0]), 0x00, frame[1])...)
		records = append(records, rawEvent(evSyn, 0, 0)...)
	}
	if err := device.write(records); err != nil {
		t.Fatal(err)
	}
}
