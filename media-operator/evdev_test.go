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

func TestBitmapsMatchTheBoundCodes(t *testing.T) {
	bindings := []compiledBinding{
		{EventType: evKey, Code: 0x130, Value: 1, Action: actionPause},
		{EventType: evAbs, Code: 0x11, Value: -1, Action: actionVolume, Amount: 5},
	}

	cases := []struct {
		name string
		keys []byte
		axes []byte
		want bool
	}{
		{
			name: "a gamepad with the button and the hat",
			keys: bitmapOf(keyBitmapBytes, 0x130, 0x131),
			axes: bitmapOf(absBitmapBytes, 0x10, 0x11),
			want: true,
		},
		{
			name: "a node with the button alone",
			keys: bitmapOf(keyBitmapBytes, 0x130),
			axes: bitmapOf(absBitmapBytes),
			want: true,
		},
		{
			name: "a node with the hat alone",
			keys: bitmapOf(keyBitmapBytes),
			axes: bitmapOf(absBitmapBytes, 0x11),
			want: true,
		},
		{
			name: "a node with neither",
			keys: bitmapOf(keyBitmapBytes, 0x1c),
			axes: bitmapOf(absBitmapBytes, 0x10),
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
			mustMatch(t, bitmapsMatch(bindings, each.keys, each.axes), each.want)
		})
	}
}

func TestBitmapsMatchNothingWithoutBindings(t *testing.T) {
	mustMatch(t, bitmapsMatch(nil, bitmapOf(keyBitmapBytes, 0x130), bitmapOf(absBitmapBytes, 0x11)), false)
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
