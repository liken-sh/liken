package main

// The record layout and the reading of it are pure, so they test
// without the device; only the open and the write need the real
// character device. The layout is worth pinning byte by byte, because
// a wrong byte here is the failure that would look like a radio that
// is simply out of range.

import (
	"bytes"
	"strings"
	"testing"
)

func TestRfkillEventEncodesTheKernelsLayout(t *testing.T) {
	got := rfkillEvent{index: 0x01020304, kind: rfkillTypeWLAN, op: rfkillOpChangeAll, soft: 1, hard: 0}.encode()
	want := []byte{0x04, 0x03, 0x02, 0x01, 0x01, 0x03, 0x01, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("encode() = %v, want %v", got, want)
	}
}

func TestRfkillEventRoundTrips(t *testing.T) {
	want := rfkillEvent{index: 7, kind: rfkillTypeWLAN, op: rfkillOpAdd, soft: 1, hard: 1}
	got, ok := decodeRfkillEvent(want.encode())
	if !ok {
		t.Fatal("a whole record must decode")
	}
	if got != want {
		t.Errorf("decode() = %+v, want %+v", got, want)
	}
}

func TestDecodeRfkillEventRefusesAPartialRecord(t *testing.T) {
	if _, ok := decodeRfkillEvent([]byte{0, 0, 0, 0, 1, 0, 0}); ok {
		t.Error("seven bytes name no radio")
	}
}

func TestUnblockWLANClearsTheSoftBlockOnEveryWirelessRadio(t *testing.T) {
	got := unblockWLAN()
	if got.kind != rfkillTypeWLAN || got.op != rfkillOpChangeAll || got.soft != 0 {
		t.Errorf("unblockWLAN() = %+v", got)
	}
	// A change-all record carries no index, because it addresses
	// every radio of its type at once.
	if got.index != 0 {
		t.Errorf("index = %d, want 0", got.index)
	}
}

func TestDescribeRfkillStateNamesAHardBlockAsUnclearable(t *testing.T) {
	lines := describeRfkillState([]rfkillEvent{
		{index: 0, kind: rfkillTypeWLAN, op: rfkillOpAdd, hard: 1},
	})
	if len(lines) != 1 || !strings.Contains(lines[0], "switch on the machine") {
		t.Errorf("got %q", lines)
	}
}

func TestDescribeRfkillStateSkipsRadiosOfOtherKinds(t *testing.T) {
	// A Bluetooth radio is another kind on the same device, and this
	// boot has nothing to say about it.
	const bluetooth = 2
	lines := describeRfkillState([]rfkillEvent{
		{index: 0, kind: bluetooth, op: rfkillOpAdd, soft: 1},
		{index: 1, kind: rfkillTypeWLAN, op: rfkillOpAdd, soft: 1},
	})
	if len(lines) != 1 || !strings.Contains(lines[0], "radio 1") {
		t.Errorf("got %q", lines)
	}
}

func TestDescribeRfkillStateSkipsRecordsThatAreNotAdditions(t *testing.T) {
	lines := describeRfkillState([]rfkillEvent{
		{index: 0, kind: rfkillTypeWLAN, op: rfkillOpChangeAll, soft: 1},
	})
	if len(lines) != 0 {
		t.Errorf("got %q", lines)
	}
}

func TestReadRfkillEventsReadsEveryQueuedRecord(t *testing.T) {
	queued := []rfkillEvent{
		{index: 0, kind: rfkillTypeWLAN, op: rfkillOpAdd, soft: 1},
		{index: 1, kind: rfkillTypeWLAN, op: rfkillOpAdd},
	}
	var raw []byte
	for _, e := range queued {
		raw = append(raw, e.encode()...)
	}
	got := readRfkillEvents(bytes.NewReader(raw))
	if len(got) != 2 || got[0] != queued[0] || got[1] != queued[1] {
		t.Errorf("got %+v, want %+v", got, queued)
	}
}
