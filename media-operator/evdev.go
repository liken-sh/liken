package main

// The kernel publishes every input device as a character node under
// /dev/input, and a read on one returns fixed-width event records:
// a button went down, an axis moved. The records are a stable
// kernel ABI, so the sidecar reads them directly rather than through
// an input library, which would be the player image's only reason to
// carry one.
//
// One controller publishes several nodes. A DualSense publishes
// three: the buttons and sticks, the touchpad, and the motion
// sensors, and only the first is worth reading. The node's name is
// per-model trivia, but the kernel also publishes each node's
// capability bitmaps, one bit per event code it can report. The
// standing remote pod carries no keymap, so the node it keeps is the
// one that reports any button or hat axis a Keymap could bind, and the
// buttonCodes and axisCodes tables are that whole vocabulary. That
// selection rule needs no model knowledge at all.

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// inputEvent is the kernel's struct input_event on a 64-bit
// machine: a timestamp, the event type, the code, and the value.
// The timestamp goes unread, because the sidecar acts on a press
// the moment it arrives and never orders or replays events.
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

// A record is 24 bytes on a 64-bit kernel: sixteen of timestamp,
// two of type, two of code, four of value. The kernel returns whole
// records from every read, never a fraction of one.
const inputEventSize = 24

// The bitmap sizes, one bit per code: KEY_MAX is 0x2ff, so 96 bytes
// cover every key code, and ABS_MAX is 0x3f, so 8 bytes cover every
// axis.
const (
	keyBitmapBytes = 96
	absBitmapBytes = 8
)

// parseInputEvents decodes one read's worth of records. The fields
// are little endian, which is correct on the amd64 and arm64
// machines liken targets and wrong on a big-endian kernel. A tail
// shorter than one record is ignored; the kernel does not split
// records, so there is never one to carry over.
func parseInputEvents(buffer []byte) []inputEvent {
	events := make([]inputEvent, 0, len(buffer)/inputEventSize)
	for offset := 0; offset+inputEventSize <= len(buffer); offset += inputEventSize {
		record := buffer[offset : offset+inputEventSize]
		events = append(events, inputEvent{
			Sec:   int64(binary.LittleEndian.Uint64(record[0:8])),
			Usec:  int64(binary.LittleEndian.Uint64(record[8:16])),
			Type:  binary.LittleEndian.Uint16(record[16:18]),
			Code:  binary.LittleEndian.Uint16(record[18:20]),
			Value: int32(binary.LittleEndian.Uint32(record[20:24])),
		})
	}
	return events
}

// bitmapRequest builds the EVIOCGBIT ioctl number by hand, the way
// the C macro _IOC does: two bits of direction (read), fourteen bits
// of size, the event family's letter 'E', and 0x20 plus the event
// type as the command. The size is part of the number, so it must
// state the buffer's real length or the kernel copies the wrong
// amount.
func bitmapRequest(evType uint16, length int) uintptr {
	return uintptr(2)<<30 | uintptr(length)<<16 | uintptr(0x45)<<8 | uintptr(0x20+evType)
}

// bitmapHasCode reads one code's bit: byte code/8, bit code%8,
// least significant first. A code past the bitmap's end is a code
// the node does not report.
func bitmapHasCode(bitmap []byte, code uint16) bool {
	index := int(code / 8)
	if index >= len(bitmap) {
		return false
	}
	return bitmap[index]&(1<<(code%8)) != 0
}

// reportsAnyCode reports whether the node's bitmap sets a bit for any
// code in the table. One code is enough, because a controller's button
// node reports every button in buttonCodes, and its d-pad node reports
// every hat axis in axisCodes, while the touchpad and the motion
// sensors report none of either.
func reportsAnyCode(bitmap []byte, codes map[string]uint16) bool {
	for _, code := range codes {
		if bitmapHasCode(bitmap, code) {
			return true
		}
	}
	return false
}

// controllerBitmaps is the selection rule: a node is a controller when
// it reports any button in buttonCodes or any hat axis in axisCodes.
// The reader has no keymap, so it keeps every node that could carry a
// bound code rather than the node one keymap needs, and one standing
// pod then feeds two players that map the controller differently.
func controllerBitmaps(keys, axes []byte) bool {
	return reportsAnyCode(keys, buttonCodes) || reportsAnyCode(axes, axisCodes)
}

// isController asks the node for its key and axis bitmaps and applies
// the selection rule. The claim already narrowed /dev/input to one
// controller's nodes, but one controller is still several nodes, and
// this test picks the one that carries the buttons. A node that
// refuses the ioctl is not an event device and matches nothing.
func isController(descriptor int) bool {
	keys, err := deviceBitmap(descriptor, evKey, keyBitmapBytes)
	if err != nil {
		return false
	}
	axes, err := deviceBitmap(descriptor, evAbs, absBitmapBytes)
	if err != nil {
		return false
	}
	return controllerBitmaps(keys, axes)
}

// deviceBitmap fetches one event type's capability bitmap. The
// kernel fills the buffer up to the length the request number
// states and reports how much it wrote; only the bits matter here.
func deviceBitmap(descriptor int, evType uint16, length int) ([]byte, error) {
	bitmap := make([]byte, length)
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(descriptor),
		bitmapRequest(evType, length),
		uintptr(unsafe.Pointer(&bitmap[0])),
	)
	if errno != 0 {
		return nil, errno
	}
	return bitmap, nil
}
