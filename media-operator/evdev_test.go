package main

import (
	"encoding/binary"
	"testing"
)

func TestParseInputEvents(t *testing.T) {
	press := inputEvent{Sec: 1_700_000_000, Usec: 250_000, Type: evKey, Code: 0x130, Value: 1}
	release := inputEvent{Sec: 1_700_000_000, Usec: 310_000, Type: evKey, Code: 0x130, Value: 0}
	hat := inputEvent{Sec: 1_700_000_001, Usec: 5, Type: evAbs, Code: 0x11, Value: -1}

	cases := []struct {
		name  string
		bytes []byte
		want  []inputEvent
	}{
		{
			name:  "an empty read",
			bytes: nil,
			want:  nil,
		},
		{
			name:  "one press",
			bytes: eventBytes(press),
			want:  []inputEvent{press},
		},
		{
			name:  "a press, its release, and a hat",
			bytes: eventBytes(press, release, hat),
			want:  []inputEvent{press, release, hat},
		},
		{
			name:  "a record the read cut in half",
			bytes: append(eventBytes(press), eventBytes(hat)[:10]...),
			want:  []inputEvent{press},
		},
		{
			name:  "fewer bytes than one record",
			bytes: eventBytes(press)[:23],
			want:  nil,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, parseInputEvents(each.bytes), each.want)
		})
	}
}

func TestBitmapCarriesOneBitPerCode(t *testing.T) {
	bitmap := bitmapOf(keyBitmapBytes, 0x130, 0x13b, 0x2ff)

	cases := []struct {
		name string
		code uint16
		want bool
	}{
		{name: "the first bit of a byte", code: 0x130, want: true},
		{name: "a later bit of the same byte", code: 0x13b, want: true},
		{name: "the last code the bitmap holds", code: 0x2ff, want: true},
		{name: "a code beside one that is set", code: 0x131, want: false},
		{name: "a code the bitmap does not reach", code: 0x400, want: false},
		{name: "code zero", code: 0x000, want: false},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, bitmapHasCode(bitmap, each.code), each.want)
		})
	}
}

// The reader keeps any node that declares a key code or a hat axis,
// because a Keymap may bind either and the reader holds no keymap to
// narrow it. A node that declares neither is not a controller.
func TestControllerBitmapsKeepTheNodeThatCarriesInput(t *testing.T) {
	cases := []struct {
		name string
		keys []byte
		axes []byte
		want bool
	}{
		{
			name: "a gamepad with buttons and the hat",
			keys: bitmapOf(keyBitmapBytes, 0x130, 0x131),
			axes: bitmapOf(absBitmapBytes, 0x10, 0x11),
			want: true,
		},
		{
			name: "a node with one button alone",
			keys: bitmapOf(keyBitmapBytes, 0x130),
			axes: bitmapOf(absBitmapBytes),
			want: true,
		},
		{
			name: "a node with one hat axis alone",
			keys: bitmapOf(keyBitmapBytes),
			axes: bitmapOf(absBitmapBytes, 0x11),
			want: true,
		},
		{
			name: "a media remote that reports transport keys alone",
			keys: bitmapOf(keyBitmapBytes, 0x0a4, 0x160),
			axes: bitmapOf(absBitmapBytes),
			want: true,
		},
		{
			name: "a touchpad that reports a touch button",
			keys: bitmapOf(keyBitmapBytes, 0x14a),
			axes: bitmapOf(absBitmapBytes, 0x35),
			want: true,
		},
		{
			name: "motion sensors, which report no key code at all",
			keys: bitmapOf(keyBitmapBytes),
			axes: bitmapOf(absBitmapBytes, 0x00, 0x01, 0x02),
			want: false,
		},
		{
			name: "a node that advertises nothing",
			keys: bitmapOf(keyBitmapBytes),
			axes: bitmapOf(absBitmapBytes),
			want: false,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, controllerBitmaps(each.keys, each.axes), each.want)
		})
	}
}

func TestBitmapRequestNumbers(t *testing.T) {
	cases := []struct {
		name   string
		evType uint16
		length int
		want   uintptr
	}{
		{name: "the key bitmap", evType: evKey, length: keyBitmapBytes, want: 0x80604521},
		{name: "the absolute axis bitmap", evType: evAbs, length: absBitmapBytes, want: 0x80084523},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, bitmapRequest(each.evType, each.length), each.want)
		})
	}
}

// eventBytes writes events in the kernel's own record layout, 24
// little-endian bytes each, which is what a read on an event node
// returns.
func eventBytes(events ...inputEvent) []byte {
	written := make([]byte, 0, len(events)*inputEventSize)
	for _, event := range events {
		record := make([]byte, inputEventSize)
		binary.LittleEndian.PutUint64(record[0:8], uint64(event.Sec))
		binary.LittleEndian.PutUint64(record[8:16], uint64(event.Usec))
		binary.LittleEndian.PutUint16(record[16:18], event.Type)
		binary.LittleEndian.PutUint16(record[18:20], event.Code)
		binary.LittleEndian.PutUint32(record[20:24], uint32(event.Value))
		written = append(written, record...)
	}
	return written
}

// bitmapOf is one node's answer to EVIOCGBIT: a bit set for each
// code the node reports.
func bitmapOf(length int, codes ...uint16) []byte {
	bitmap := make([]byte, length)
	for _, code := range codes {
		bitmap[code/8] |= 1 << (code % 8)
	}
	return bitmap
}

// The reader requests each node's name from the kernel, and the ioctl
// number carries the buffer's length the way the bitmap requests do.
func TestNameRequestNumber(t *testing.T) {
	mustMatch(t, nameRequest(nodeNameBytes), uintptr(0x80804506))
}

// The kernel writes the name with a trailing NUL and reports the bytes
// it wrote, so the string ends at the NUL and an empty answer is no
// name.
func TestTheNodeNameEndsAtTheKernelsNul(t *testing.T) {
	cases := []struct {
		name    string
		buffer  []byte
		written int
		want    string
	}{
		{
			name:    "a name and its NUL",
			buffer:  append([]byte("Wireless Controller\x00"), make([]byte, 20)...),
			written: 20,
			want:    "Wireless Controller",
		},
		{
			name:    "a name the kernel did not terminate",
			buffer:  []byte("Wireless Controller"),
			written: 19,
			want:    "Wireless Controller",
		},
		{
			name:    "an answer of nothing",
			buffer:  make([]byte, 16),
			written: 0,
			want:    "",
		},
		{
			name:    "a length past the buffer",
			buffer:  []byte("pad"),
			written: 64,
			want:    "pad",
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, nodeName(each.buffer, each.written), each.want)
		})
	}
}

// The declared codes are what the node's bitmaps state it can report,
// read with no button pressed, in code order.
func TestTheDeclaredCodesComeOffTheBitmaps(t *testing.T) {
	mustMatchAll(t, declaredKeyCodes(bitmapOf(keyBitmapBytes, 0x131, 0x130, 0x2ff)),
		[]uint16{0x130, 0x131, 0x2ff})
	mustMatchAll(t, declaredKeyCodes(bitmapOf(keyBitmapBytes)), nil)
	mustMatchAll(t, declaredHatAxes(bitmapOf(absBitmapBytes, 0x00, 0x10, 0x11)),
		[]uint16{0x10, 0x11})
	mustMatchAll(t, declaredHatAxes(bitmapOf(absBitmapBytes, 0x00, 0x01)), nil)
}

// One line per node carries the decision and the counts behind it, so
// a controller that reaches nothing shows why.
func TestTheVerdictLineCarriesTheDecisionAndItsCounts(t *testing.T) {
	cases := []struct {
		name string
		node nodeVerdict
		want string
	}{
		{
			name: "a gamepad's button node",
			node: nodeVerdict{
				Path: "/dev/input/event3", Name: "Wireless Controller",
				Keys: 52, Hats: 2, Keep: true,
			},
			want: `event3 "Wireless Controller" keep: 52 key codes, 2 hat axes`,
		},
		{
			name: "the motion sensors beside it",
			node: nodeVerdict{
				Path: "/dev/input/event5", Name: "Wireless Controller Motion Sensors",
				Keep: false,
			},
			want: `event5 "Wireless Controller Motion Sensors" reject: no key codes, no hat axes`,
		},
		{
			name: "a node with one of each",
			node: nodeVerdict{Path: "/dev/input/event9", Name: "pad", Keys: 1, Hats: 1, Keep: true},
			want: `event9 "pad" keep: 1 key code, 1 hat axis`,
		},
		{
			name: "a node the kernel names nothing",
			node: nodeVerdict{Path: "/dev/input/event9", Keys: 3, Keep: true},
			want: `event9 "" keep: 3 key codes, no hat axes`,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, each.node.line(), each.want)
		})
	}
}
