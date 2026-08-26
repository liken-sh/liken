package main

// Unblocking the radio. Machines commonly ship with their radios
// soft-blocked, and a blocked radio reports the same "no access point
// in range" a distant access point reports, which is the one failure
// this design treats as always transient. /dev/rfkill is the kernel's
// whole interface for that state. The protocol is fixed-size records
// read from and written to the device, not ioctls, so liken writes
// the records itself instead of carrying the rfkill program.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// rfkillDevice is the kernel's radio switch. It is a variable so tests
// can point the reader at a file of their own.
var rfkillDevice = "/dev/rfkill"

const (
	// struct rfkill_event is packed, and this is its original size.
	// Later kernels appended a field behind a longer struct, but the
	// kernel still accepts and still fills the original eight bytes,
	// so eight is the size that works everywhere.
	rfkillEventSize = 8

	// The type and operation numbers come from the kernel's
	// rfkill.h. Type 1 is WLAN. Change-all addresses every radio of
	// one type at once, the one operation that needs no index looked
	// up first.
	rfkillTypeWLAN    = 1
	rfkillOpAdd       = 0
	rfkillOpChangeAll = 3
)

// rfkillEvent is one record of the rfkill protocol, read from the
// device or written to it. The two block fields answer different
// questions: soft is the state software owns, and hard is a physical
// switch or a firmware interlock that no write can clear.
type rfkillEvent struct {
	index uint32
	kind  uint8
	op    uint8
	soft  uint8
	hard  uint8
}

// encode renders one record in the kernel's layout: little-endian on
// every architecture liken runs on, and packed with no padding.
func (e rfkillEvent) encode() []byte {
	buf := make([]byte, rfkillEventSize)
	binary.LittleEndian.PutUint32(buf[0:4], e.index)
	buf[4] = e.kind
	buf[5] = e.op
	buf[6] = e.soft
	buf[7] = e.hard
	return buf
}

// decodeRfkillEvent reads one record out of a buffer. A short buffer
// yields nothing, because a partial record names no radio.
func decodeRfkillEvent(buf []byte) (rfkillEvent, bool) {
	if len(buf) < rfkillEventSize {
		return rfkillEvent{}, false
	}
	return rfkillEvent{
		index: binary.LittleEndian.Uint32(buf[0:4]),
		kind:  buf[4],
		op:    buf[5],
		soft:  buf[6],
		hard:  buf[7],
	}, true
}

// unblockWLAN is the record that clears the soft block on every
// wireless radio at once.
//
// unblockWLAN uses change-all rather than one record per radio. A
// per-radio record needs the index that an add record assigned, and a
// machine with two radios would need two lookups and two writes.
// Change-all needs neither, and liken never wants one radio blocked
// while another is clear.
func unblockWLAN() rfkillEvent {
	return rfkillEvent{kind: rfkillTypeWLAN, op: rfkillOpChangeAll, soft: 0}
}

// describeRfkillState reports what the machine's wireless radios say
// about themselves, one line for each. It reads only the add records,
// which is what the kernel queues for every radio that already exists
// when a program opens the device.
//
// describeRfkillState gives a hard block its own line because a write
// cannot clear one. The supplicant then reports the same "no access
// point in range" a distant access point reports, and only this line
// tells the person at the machine to look for a physical switch.
func describeRfkillState(events []rfkillEvent) []string {
	var lines []string
	for _, e := range events {
		if e.op != rfkillOpAdd || e.kind != rfkillTypeWLAN {
			continue
		}
		switch {
		case e.hard != 0:
			lines = append(lines, fmt.Sprintf("liken: wireless: radio %d is blocked by a switch on the machine; software cannot clear it", e.index))
		case e.soft != 0:
			lines = append(lines, fmt.Sprintf("liken: wireless: radio %d is soft-blocked; unblocking it", e.index))
		default:
			lines = append(lines, fmt.Sprintf("liken: wireless: radio %d is already unblocked", e.index))
		}
	}
	return lines
}

// unblockRadios opens the kernel's radio switch, reports the state of
// every wireless radio it finds, and clears the soft block on all of
// them. Init calls it once, before it starts a supplicant, and only
// when the spec names a wireless interface.
//
// unblockRadios opens the device non-blocking because the kernel
// queues one add record for each radio at open time and nothing more
// until a radio changes. A blocking read would hold the boot for as
// long as nothing changed.
func unblockRadios() error {
	fd, err := unix.Open(rfkillDevice, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("opening %s: %w", rfkillDevice, err)
	}
	device := os.NewFile(uintptr(fd), rfkillDevice)
	defer device.Close()

	for _, line := range describeRfkillState(readRfkillEvents(device)) {
		fmt.Println(line)
	}

	if _, err := device.Write(unblockWLAN().encode()); err != nil {
		return fmt.Errorf("unblocking the wireless radios: %w", err)
	}
	return nil
}

// readRfkillEvents drains the records the kernel has queued. It stops
// at the first read that would block, which is the point where the
// kernel has described every radio the machine has.
func readRfkillEvents(r io.Reader) []rfkillEvent {
	var events []rfkillEvent
	buf := make([]byte, rfkillEventSize)
	for {
		n, err := r.Read(buf)
		if err != nil || n == 0 {
			// EAGAIN is the ordinary end of the queue. Any other
			// failure leaves the state unreported but still lets
			// the write below run, because the unblock matters more
			// than the report.
			if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, io.EOF) {
				fmt.Fprintf(os.Stderr, "liken: wireless: reading %s: %v\n", rfkillDevice, err)
			}
			return events
		}
		event, ok := decodeRfkillEvent(buf[:n])
		if !ok {
			return events
		}
		events = append(events, event)
	}
}
