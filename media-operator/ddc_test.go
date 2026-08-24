package main

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestDDCGetRequest(t *testing.T) {
	mustMatchAll(t, ddcGetRequest(vcpBrightness), []byte{0x51, 0x82, 0x01, 0x10, 0xac})
}

func TestDDCSetRequest(t *testing.T) {
	cases := []struct {
		name  string
		code  byte
		value uint16
		want  []byte
	}{
		{
			name:  "brightness to zero",
			code:  vcpBrightness,
			value: 0,
			want:  []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x00, 0xa8},
		},
		{
			name:  "power mode to off",
			code:  vcpPowerMode,
			value: 4,
			want:  []byte{0x51, 0x84, 0x03, 0xd6, 0x00, 0x04, 0x6a},
		},
		{
			name:  "brightness to 100",
			code:  vcpBrightness,
			value: 100,
			want:  []byte{0x51, 0x84, 0x03, 0x10, 0x00, 0x64, 0xcc},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatchAll(t, ddcSetRequest(each.code, each.value), each.want)
		})
	}
}

func TestParseDDCGetReplySucceeds(t *testing.T) {
	value, err := parseDDCGetReply(goodGetReply(), vcpBrightness)
	mustSucceed(t, err)
	mustMatch(t, value, uint16(50))
}

func TestParseDDCGetReplyFailures(t *testing.T) {
	cases := []struct {
		name  string
		reply []byte
		want  error
	}{
		{name: "all ones", reply: bytes.Repeat([]byte{0xff}, ddcGetReplyLength), want: ErrDDCNoAnswer},
		{name: "all zeros", reply: bytes.Repeat([]byte{0x00}, ddcGetReplyLength), want: ErrDDCNoAnswer},
		{name: "wrong source", reply: withByte(goodGetReply(), 0, 0x11), want: ErrDDCGarbledReply},
		{name: "wrong length byte", reply: withByte(goodGetReply(), 1, 0x87), want: ErrDDCGarbledReply},
		{name: "wrong opcode", reply: withByte(goodGetReply(), 2, 0x01), want: ErrDDCGarbledReply},
		{name: "bad checksum", reply: withByte(goodGetReply(), 10, 0x00), want: ErrDDCGarbledReply},
		{name: "unsupported code", reply: unsupportedGetReply(), want: ErrDDCUnsupportedVCP},
		{name: "another code's reply", reply: withByteResummed(goodGetReply(), 4, 0xd6), want: ErrDDCGarbledReply},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			_, err := parseDDCGetReply(each.reply, vcpBrightness)
			mustBeError(t, err, each.want)
		})
	}
}

func TestSetVCPWritesTheRequestAndWaits(t *testing.T) {
	bus := &scriptedDDCBus{}
	wire := &ddcWire{bus: bus, sleep: noSleep(t)}

	mustSucceed(t, wire.SetVCP(vcpPowerMode, powerModeOff))

	mustMatchAll(t, bus.writes[0], ddcSetRequest(vcpPowerMode, powerModeOff))
}

func TestGetVCPReadsTheReply(t *testing.T) {
	bus := &scriptedDDCBus{replies: [][]byte{goodGetReply()}}
	wire := &ddcWire{bus: bus, sleep: noSleep(t)}

	value, err := wire.GetVCP(vcpBrightness)

	mustSucceed(t, err)
	mustMatch(t, value, uint16(50))
	mustMatchAll(t, bus.writes[0], ddcGetRequest(vcpBrightness))
}

// A garbled first reply gives way to a clean second one, and the
// retry gap is a stubbed sleep, so no test waits it out.
func TestGetVCPRetriesAfterAGarbledReply(t *testing.T) {
	bus := &scriptedDDCBus{replies: [][]byte{
		withByte(goodGetReply(), 2, 0x01),
		goodGetReply(),
	}}
	wire := &ddcWire{bus: bus, sleep: noSleep(t)}

	value, err := wire.GetVCP(vcpBrightness)

	mustSucceed(t, err)
	mustMatch(t, value, uint16(50))
	mustMatch(t, len(bus.writes), 2)
}

func TestGetVCPStopsRetryingOnUnsupportedCode(t *testing.T) {
	bus := &scriptedDDCBus{replies: [][]byte{unsupportedGetReply()}}
	wire := &ddcWire{bus: bus, sleep: noSleep(t)}

	_, err := wire.GetVCP(vcpBrightness)

	mustBeError(t, err, ErrDDCUnsupportedVCP)
	mustMatch(t, len(bus.writes), 1)
}

// goodGetReply is a Get reply for vcpBrightness, current 50 of max
// 100.
func goodGetReply() []byte {
	return []byte{0x6e, 0x88, 0x02, 0x00, 0x10, 0x00, 0x00, 0x64, 0x00, 0x32, 0xf2}
}

// unsupportedGetReply is the same reply, with its result byte naming
// the code absent.
func unsupportedGetReply() []byte {
	return []byte{0x6e, 0x88, 0x02, 0x01, 0x10, 0x00, 0x00, 0x64, 0x00, 0x32, 0xf3}
}

// withByte is a copy of bytes with one index changed, for one-field
// cases.
func withByte(source []byte, index int, value byte) []byte {
	copied := append([]byte(nil), source...)
	copied[index] = value
	return copied
}

// withByteResummed is a copy with one byte changed and the checksum
// made good again, for a case that must fail on the field and not on
// the sum.
func withByteResummed(source []byte, index int, value byte) []byte {
	copied := withByte(source, index, value)
	last := len(copied) - 1
	copied[last] = ddcChecksum(ddcVirtualHost, copied[:last])
	return copied
}

func mustBeError(t *testing.T, err error, target error) {
	t.Helper()
	mustMatch(t, errors.Is(err, target), true)
}

// noSleep is a sleep stub, so no test waits out a real DDC/CI
// delay.
func noSleep(t *testing.T) func(time.Duration) {
	t.Helper()
	return func(time.Duration) {}
}

// scriptedDDCBus is an in-memory bus: replies is what GetVCP reads
// back, in order, and writes is every request SetVCP or GetVCP
// sent.
type scriptedDDCBus struct {
	replies [][]byte
	writes  [][]byte
	reads   int
}

func (b *scriptedDDCBus) Write(request []byte) error {
	b.writes = append(b.writes, append([]byte(nil), request...))
	return nil
}

func (b *scriptedDDCBus) Read(reply []byte) error {
	copy(reply, b.replies[b.reads])
	b.reads++
	return nil
}
