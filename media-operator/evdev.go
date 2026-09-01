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
// sensors. The kernel also publishes each node's capability bitmaps,
// one bit per event code it can report, and the node's name. The
// standing pod carries no keymap, so it keeps any node that declares a
// key code or a hat axis, which is every node a Keymap could bind: a
// gamepad's buttons and a media remote's keys alike. It logs one
// verdict line per node, so a node the rule rejects is legible in the
// pod log.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// The buffer the kernel writes a node's name into. A longer name is
// cut at the buffer's end, which still tells one node from another.
const nodeNameBytes = 128

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

// declaredKeyCodes lists every key code the node declares, in code
// order. The bitmap is read with no button pressed, so the set is
// complete the moment the node opens.
func declaredKeyCodes(bitmap []byte) []uint16 {
	var codes []uint16
	for code := range uint16(len(bitmap) * 8) {
		if bitmapHasCode(bitmap, code) {
			codes = append(codes, code)
		}
	}
	return codes
}

// declaredHatAxes lists the hat axes the node declares, in code order.
// The hats are the only axes a Keymap binds.
func declaredHatAxes(bitmap []byte) []uint16 {
	var codes []uint16
	for _, code := range axisCodes {
		if bitmapHasCode(bitmap, code) {
			codes = append(codes, code)
		}
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return codes
}

// controllerBitmaps is the selection rule: any key code at all, or
// either hat axis. The reader has no keymap, so it keeps every node
// that could carry a bound code, and one standing pod then feeds two
// players that map the controller differently.
func controllerBitmaps(keys, axes []byte) bool {
	return len(declaredKeyCodes(keys)) > 0 || len(declaredHatAxes(axes)) > 0
}

// nodeVerdict is what the reader decided about one node, with the
// counts behind the decision. The verdicts log in both modes, so a
// node the rule rejects is legible in the pod log.
type nodeVerdict struct {
	Path string
	Name string
	Keys int
	Hats int
	Keep bool
}

// line renders one verdict the way the pod logs it.
func (v nodeVerdict) line() string {
	verdict := "reject"
	if v.Keep {
		verdict = "keep"
	}
	return fmt.Sprintf("%s %q %s: %s, %s",
		filepath.Base(v.Path), v.Name, verdict,
		countOf(v.Keys, "key code", "key codes"),
		countOf(v.Hats, "hat axis", "hat axes"))
}

// countOf writes one count in words, so a node that declares nothing
// states that plainly.
func countOf(count int, singular, plural string) string {
	switch count {
	case 0:
		return "no " + plural
	case 1:
		return "1 " + singular
	default:
		return fmt.Sprintf("%d %s", count, plural)
	}
}

// openNode is one node the reader inspected: the file it reads, the
// path and name a person knows it by, and the two capability bitmaps,
// read once at open because no press changes them.
type openNode struct {
	file *os.File
	path string
	name string
	keys []byte
	axes []byte
}

// label is the node as every logged line names it.
func (n openNode) label() string {
	return fmt.Sprintf("%s %q", filepath.Base(n.path), n.name)
}

// verdict is this node's decision, with the counts behind it.
func (n openNode) verdict(keep bool) nodeVerdict {
	return nodeVerdict{
		Path: n.path,
		Name: n.name,
		Keys: len(declaredKeyCodes(n.keys)),
		Hats: len(declaredHatAxes(n.axes)),
		Keep: keep,
	}
}

// inspectNode reads one node's name and both capability bitmaps. A
// node that refuses either ioctl is not an event device, and the
// reader skips it in either mode.
func inspectNode(descriptor int, path string) (openNode, bool) {
	keys, err := deviceBitmap(descriptor, evKey, keyBitmapBytes)
	if err != nil {
		return openNode{}, false
	}
	axes, err := deviceBitmap(descriptor, evAbs, absBitmapBytes)
	if err != nil {
		return openNode{}, false
	}
	// A node with no name is still readable, so a missing name is no
	// reason to skip it.
	name, _ := deviceName(descriptor)
	return openNode{path: path, name: name, keys: keys, axes: axes}, true
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

// nameRequest builds the EVIOCGNAME ioctl number by hand, the way
// bitmapRequest builds EVIOCGBIT. The command is 0x06, and the size in
// the number states how much name the kernel may write.
func nameRequest(length int) uintptr {
	return uintptr(2)<<30 | uintptr(length)<<16 | uintptr(0x45)<<8 | uintptr(0x06)
}

// deviceName reads one node's name from the kernel, which is the
// model's own string, such as a controller's product name.
func deviceName(descriptor int) (string, error) {
	buffer := make([]byte, nodeNameBytes)
	written, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(descriptor),
		nameRequest(nodeNameBytes),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if errno != 0 {
		return "", errno
	}
	return nodeName(buffer, int(written)), nil
}

// nodeName decodes what the kernel wrote: the bytes it reported,
// ending at the first NUL.
func nodeName(buffer []byte, written int) string {
	if written > len(buffer) {
		written = len(buffer)
	}
	name := buffer[:written]
	for index, character := range name {
		if character == 0 {
			return string(name[:index])
		}
	}
	return string(name)
}
