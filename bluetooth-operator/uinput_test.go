package main

// Some of these tests compare each ioctl number that the code computes
// with the number that a C program compiled against linux/input.h or
// linux/uinput.h sends, because a wrong number calls a different driver
// function or none. The others create a uinput device on the real
// kernel, and skip when /dev/uinput cannot be opened, as in CI.

import (
	"encoding/binary"
	"os"
	"slices"
	"testing"
	"time"
)

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

// A struct input_event is 24 bytes on a 64-bit kernel. The relay
// writes only whole records and keeps a partial record for the next
// read, so a wrong size would split events.
func TestOneEventRecordIsTwentyFourBytes(t *testing.T) {
	if inputEventSize != 24 {
		t.Errorf("inputEventSize = %d, want 24", inputEventSize)
	}
}

func TestWithinContainerNodes(t *testing.T) {
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
		if got := withinContainerNodes(c.node); got != c.want {
			t.Errorf("withinDeliveredRange(%q) = %v, want %v", c.node, got, c.want)
		}
	}
}

// testCapabilities is a small gamepad with one button and one stick
// axis.
func testCapabilities() evdevCapabilities {
	return evdevCapabilities{
		Name: "Wireless Controller",
		ID:   evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6, Version: 0x8111},
		Codes: map[string][]uint16{
			"EV_KEY": {0x130},
			"EV_ABS": {0x00},
		},
		// Fuzz and flat are set so the read-back test checks that the
		// virtual axis copies them.
		Axes: []absAxis{{Code: 0x00, Minimum: 0, Maximum: 255, Fuzz: 3, Flat: 15}},
	}
}

// The kernel creates the device and its node, and removes the node
// when the fd closes.
func TestCreateAVirtualDeviceOnTheRealKernel(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(testCapabilities(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatalf("creating the virtual device: %v", err)
	}
	node := device.node()
	if _, err := os.Stat(node); err != nil {
		t.Fatalf("the node %s that the kernel reported does not exist: %v", node, err)
	}
	// The relay copies whole records without decoding them, so one
	// record is a sufficient write.
	if err := device.write(make([]byte, inputEventSize)); err != nil {
		t.Errorf("writing one event: %v", err)
	}

	if err := device.close(); err != nil {
		t.Fatalf("closing the virtual device: %v", err)
	}
	if _, err := os.Stat(node); !os.IsNotExist(err) {
		t.Errorf("after the device closed, stat %s = %v, want it absent", node, err)
	}
}

// The relay reads a real node's capabilities and creates a virtual
// device from them, so the kernel must report back the capabilities
// that the device was created with.
func TestReadBackTheCapabilitiesOfADeviceThisTestCreated(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(testCapabilities(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatalf("creating the virtual device: %v", err)
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
		t.Errorf("EV_KEY codes = %v, want [0x130]", caps.Codes["EV_KEY"])
	}
	if len(caps.Axes) != 1 || caps.Axes[0] != want.Axes[0] {
		t.Errorf("axes = %+v, want %+v", caps.Axes, want.Axes)
	}
}

// rawEvent returns one struct input_event: two timestamp words, left at
// zero, then the type, the code, and the value.
func rawEvent(eventType, code uint16, value int32) []byte {
	record := make([]byte, inputEventSize)
	binary.NativeEndian.PutUint16(record[16:], eventType)
	binary.NativeEndian.PutUint16(record[18:], code)
	binary.NativeEndian.PutUint32(record[20:], uint32(value))
	return record
}

// readEventTypes returns the type of each record in one read from node.
func readEventTypes(t *testing.T, node realNode) []uint16 {
	t.Helper()
	var types []uint16
	for at, got := 0, readEventRecords(t, node); at+inputEventSize <= len(got); at += inputEventSize {
		types = append(types, binary.NativeEndian.Uint16(got[at+16:]))
	}
	return types
}

// readEventRecords returns the bytes of one read from node. A read
// that never returns means a mask or an axis dropped more than the test
// intended, so the test fails after two seconds.
func readEventRecords(t *testing.T, node realNode) []byte {
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

// The kernel drops events that a mask excludes before it queues them,
// so an event type that no claim asked for never wakes the relay's
// read loop.
func TestMaskARealNodeOnTheRealKernel(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(testCapabilities(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatalf("creating the virtual device: %v", err)
	}
	t.Cleanup(func() { _ = device.close() })
	node, err := linuxInput{}.open(device.node())
	if err != nil {
		// The operator opens controller nodes as root. A workstation
		// user is usually not in the group that owns them.
		t.Skipf("this machine does not permit reading %s: %v", device.node(), err)
	}
	t.Cleanup(func() { _ = node.Close() })

	// Without a mask, the node returns every event type that the device
	// declares.
	frame := append(rawEvent(evAbs, 0x00, 100), rawEvent(evSyn, 0, 0)...)
	if err := device.write(frame); err != nil {
		t.Fatalf("writing the frame to the virtual device: %v", err)
	}
	if types := readEventTypes(t, node); !slices.Contains(types, uint16(evAbs)) {
		t.Fatalf("the unmasked node delivered types %v, want an absolute event", types)
	}

	if err := node.setMasks([]eventMask{{event: evSyn, max: eventTypeMax, codes: []uint16{evKey}}}); err != nil {
		t.Fatalf("narrowing the node: %v", err)
	}

	// The kernel also drops the SYN_REPORT of a frame that the mask
	// emptied, so the absolute frame disappears completely.
	frame = append(rawEvent(evAbs, 0x00, 200), rawEvent(evSyn, 0, 0)...)
	frame = append(frame, rawEvent(evKey, 0x130, 1)...)
	frame = append(frame, rawEvent(evSyn, 0, 0)...)
	if err := device.write(frame); err != nil {
		t.Fatalf("writing the frame to the virtual device: %v", err)
	}
	types := readEventTypes(t, node)
	if slices.Contains(types, uint16(evAbs)) {
		t.Errorf("the narrowed node delivered types %v, want no absolute event", types)
	}
	if !slices.Contains(types, uint16(evKey)) {
		t.Errorf("the narrowed node delivered types %v, want the key event", types)
	}
}

// steadyAxis has one axis with no fuzz or flat, so the kernel passes
// every change of position, however small.
func steadyAxis() evdevCapabilities {
	return evdevCapabilities{
		Name:  "Wireless Controller",
		ID:    evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{"EV_KEY": {0x130}, "EV_ABS": {0x00}},
		Axes:  []absAxis{{Code: 0x00, Minimum: 0, Maximum: 255}},
	}
}

// readAxisValues returns the value of each EV_ABS record in one read
// from node.
func readAxisValues(t *testing.T, node realNode) []int32 {
	t.Helper()
	var values []int32
	for at, got := 0, readEventRecords(t, node); at+inputEventSize <= len(got); at += inputEventSize {
		if binary.NativeEndian.Uint16(got[at+16:]) != evAbs {
			continue
		}
		values = append(values, int32(binary.NativeEndian.Uint32(got[at+20:])))
	}
	return values
}

// The kernel drops a position change smaller than half the axis fuzz
// before it queues the event, so a stick that jitters at rest does not
// wake the relay's read loop.
func TestTuneARealAxisOnTheRealKernel(t *testing.T) {
	file, err := os.OpenFile(uinputPath, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("this machine does not permit %s: %v", uinputPath, err)
	}
	_ = file.Close()

	device, err := linuxInput{}.createVirtual(steadyAxis(), "bluetooth.liken.sh/a0:ab:51:33:b7:12")
	if err != nil {
		t.Fatalf("creating the virtual device: %v", err)
	}
	t.Cleanup(func() { _ = device.close() })
	node, err := linuxInput{}.open(device.node())
	if err != nil {
		t.Skipf("this machine does not permit reading %s: %v", device.node(), err)
	}
	t.Cleanup(func() { _ = node.Close() })

	// With no fuzz, a change of one step reaches the reader.
	writeFrames(t, device, [][2]int32{{evAbs, 100}, {evAbs, 101}})
	if got := readAxisValues(t, node); !slices.Contains(got, 101) {
		t.Fatalf("the untuned axis delivered %v, want the one-step change", got)
	}

	current, err := node.axisRange(0x00)
	if err != nil {
		t.Fatalf("reading the axis range: %v", err)
	}
	tuned := tunedAxis(absAxis{Code: 0x00}, current, axisOverride{Fuzz: ptr(int32(4))})
	if err := node.setAxisRange(0x00, tuned); err != nil {
		t.Fatalf("writing the axis range: %v", err)
	}
	if back, err := node.axisRange(0x00); err != nil || back.Fuzz != 4 {
		t.Fatalf("after the write, the axis has fuzz %d and error %v, want fuzz 4", back.Fuzz, err)
	}

	// After the fuzz is set, a change of one step is below half the
	// fuzz, so the kernel drops it and its frame. A larger move still
	// arrives.
	writeFrames(t, device, [][2]int32{{evAbs, 102}, {evAbs, 150}})
	got := readAxisValues(t, node)
	if slices.Contains(got, 102) {
		t.Errorf("the tuned axis delivered %v, want the one-step change dropped", got)
	}
	if !slices.Contains(got, 150) {
		t.Errorf("the tuned axis delivered %v, want the real move", got)
	}
}

// writeFrames writes one event and a SYN_REPORT for each pair of
// type and value in frames, as a device reports a position change.
func writeFrames(t *testing.T, device virtualDevice, frames [][2]int32) {
	t.Helper()
	var records []byte
	for _, frame := range frames {
		records = append(records, rawEvent(uint16(frame[0]), 0x00, frame[1])...)
		records = append(records, rawEvent(evSyn, 0, 0)...)
	}
	if err := device.write(records); err != nil {
		t.Fatalf("writing the frames to the virtual device: %v", err)
	}
}
