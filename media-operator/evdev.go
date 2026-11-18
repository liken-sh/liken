package main

// The kernel exposes each input device as a character node under
// /dev/input, and a read on a node returns fixed-size event records.
// The record layout is a stable kernel ABI, so this file parses it
// without an input library.
//
// One controller can have several nodes. A DualSense has one for the
// buttons and sticks, one for the touchpad, and one for the motion
// sensors. The reader in remote.go keeps every node whose capability
// bitmaps include a key code or a hat axis, because those are the only
// codes a Keymap can bind. The test does not look at any Keymap,
// because one pod can serve two Players that map the same controller
// differently.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"unsafe"
)

// inputEvent is the kernel's struct input_event on a 64-bit machine.
// Nothing reads the timestamp, because the reader publishes each event
// when it arrives.
type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

// A record is 24 bytes on a 64-bit kernel: 16 for the timestamp, 2 for
// the type, 2 for the code, and 4 for the value. The kernel returns
// only whole records from a read.
const inputEventSize = 24

// The capability bitmaps have one bit per code. KEY_MAX is 0x2ff, so 96
// bytes cover every key code. ABS_MAX is 0x3f, so 8 bytes cover every
// axis.
const (
	keyBitmapBytes = 96
	absBitmapBytes = 8
)

// nodeNameBytes is the size of the buffer that receives a node's name.
// The kernel cuts a longer name at the end of the buffer.
const nodeNameBytes = 128

// parseInputEvents decodes the records in one read. It reads the fields
// as little endian, which is right for the amd64 and arm64 machines
// `liken` targets and wrong on a big-endian kernel. It ignores a tail
// shorter than one record.
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

// bitmapRequest builds the EVIOCGBIT ioctl number the way the C macro
// _IOC does: 2 bits of direction (read), 14 bits of size, the evdev
// type 'E', and 0x20 plus the event type as the command. The size is
// part of the number, so it must equal the length of the buffer or the
// kernel copies the wrong number of bytes.
func bitmapRequest(evType uint16, length int) uintptr {
	return uintptr(2)<<30 | uintptr(length)<<16 | uintptr(0x45)<<8 | uintptr(0x20+evType)
}

// bitmapHasCode reports whether the bitmap has the bit for code, which
// is bit code%8 of byte code/8, counting from the least significant
// bit. A code past the end of the bitmap is not set.
func bitmapHasCode(bitmap []byte, code uint16) bool {
	index := int(code / 8)
	if index >= len(bitmap) {
		return false
	}
	return bitmap[index]&(1<<(code%8)) != 0
}

// declaredKeyCodes lists the key codes set in the bitmap, in code
// order.
func declaredKeyCodes(bitmap []byte) []uint16 {
	var codes []uint16
	for code := range uint16(len(bitmap) * 8) {
		if bitmapHasCode(bitmap, code) {
			codes = append(codes, code)
		}
	}
	return codes
}

// declaredHatAxes lists the axes of axisCodes that are set in the
// bitmap, which are the only axes a Keymap can bind. It sorts them
// because axisCodes is a map.
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

// declaresKeyOrHat reports whether a node declares at least one key
// code or one hat axis.
func declaresKeyOrHat(keys, axes []byte) bool {
	return len(declaredKeyCodes(keys)) > 0 || len(declaredHatAxes(axes)) > 0
}

// The reader sets an event mask on each node, so that the kernel
// delivers only the events that publishable in remote.go accepts. The
// motion node of a gamepad reports events continuously, even when the
// pad lies still, and reading them only to drop them wastes CPU on a
// small machine. The masks below apply the same rule as publishable,
// and a test checks that the two agree for every code.

// inputMask is the kernel's struct input_mask, the argument of
// EVIOCSMASK. One call sets the mask for one event type.
type inputMask struct {
	Type      uint32
	CodesSize uint32
	CodesPtr  uint64
}

const (
	// maskEventTypes is EV_SYN. EVIOCSMASK with this type sets the mask
	// of event types, not a mask of SYN codes.
	maskEventTypes uint16 = 0x00

	// eventTypeCount and absCodeCount are the kernel's EV_CNT and
	// ABS_CNT.
	eventTypeCount = 0x20
	absCodeCount   = 0x40

	// The kernel returns EINVAL for a mask whose length is not a
	// multiple of sizeof(long), so every mask is padded to it.
	maskWordBytes = 8
)

// maskRequest builds the EVIOCSMASK ioctl number the way bitmapRequest
// builds EVIOCGBIT. The direction is write, the size is that of
// inputMask, and the command is 0x93.
func maskRequest() uintptr {
	return uintptr(1)<<30 | uintptr(unsafe.Sizeof(inputMask{}))<<16 | uintptr(0x45)<<8 | uintptr(0x93)
}

// maskBytes returns the size of a bitmap with one bit for each of
// codes, rounded up to whole machine words.
func maskBytes(codes int) int {
	words := (codes + maskWordBytes*8 - 1) / (maskWordBytes * 8)
	return words * maskWordBytes
}

// setBitmapCode sets the bit that bitmapHasCode reads for code.
func setBitmapCode(bitmap []byte, code uint16) {
	bitmap[code/8] |= 1 << (code % 8)
}

// publishableTypes is the event type mask: EV_KEY and EV_ABS, the
// types that publishable accepts.
func publishableTypes() []byte {
	bitmap := make([]byte, maskBytes(eventTypeCount))
	setBitmapCode(bitmap, evKey)
	setBitmapCode(bitmap, evAbs)
	return bitmap
}

// publishableAxes is the EV_ABS mask: the hat axes and no other axis.
func publishableAxes() []byte {
	bitmap := make([]byte, maskBytes(absCodeCount))
	for _, code := range axisCodes {
		setBitmapCode(bitmap, code)
	}
	return bitmap
}

// setEventMask limits the events that the descriptor delivers for one
// event type to the codes in the bitmap. An empty bitmap blocks the
// whole type.
func setEventMask(descriptor int, evType uint16, codes []byte) error {
	mask := inputMask{Type: uint32(evType), CodesSize: uint32(len(codes))}
	if len(codes) > 0 {
		mask.CodesPtr = uint64(uintptr(unsafe.Pointer(&codes[0])))
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(descriptor),
		maskRequest(),
		uintptr(unsafe.Pointer(&mask)),
	)
	// CodesPtr is a plain integer, so the garbage collector does not
	// see that the kernel reads codes during the call.
	runtime.KeepAlive(codes)
	if errno != 0 {
		return errno
	}
	return nil
}

// restrictToPublishable limits a node to EV_KEY and the hat axes. The
// kernel never filters EV_SYN, so those events still arrive. When the
// kernel refuses a mask, the node delivers every event and publishable
// still drops the ones a Keymap cannot bind.
func restrictToPublishable(descriptor int) error {
	if err := setEventMask(descriptor, maskEventTypes, publishableTypes()); err != nil {
		return err
	}
	return setEventMask(descriptor, evAbs, publishableAxes())
}

// nodeVerdict records whether the reader keeps one node, with the
// number of key codes and hat axes the node declares. The reader logs
// one line for each node, so the pod log shows why a node was rejected.
type nodeVerdict struct {
	Path string
	Name string
	Keys int
	Hats int
	Keep bool
}

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

// countOf formats a count with its noun, and writes "no" and the plural
// for zero.
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

// openNode is an event node the reader has opened. The capability
// bitmaps are read once, when the node opens.
type openNode struct {
	file *os.File
	path string
	name string
	keys []byte
	axes []byte
}

// label names the node in log lines.
func (n openNode) label() string {
	return fmt.Sprintf("%s %q", filepath.Base(n.path), n.name)
}

func (n openNode) verdict(keep bool) nodeVerdict {
	return nodeVerdict{
		Path: n.path,
		Name: n.name,
		Keys: len(declaredKeyCodes(n.keys)),
		Hats: len(declaredHatAxes(n.axes)),
		Keep: keep,
	}
}

// inspectNode reads the name and both capability bitmaps of a node. It
// returns false when either bitmap ioctl fails, because then the node
// is not an event device. The reader skips such a node even in
// discovery mode.
func inspectNode(descriptor int, path string) (openNode, bool) {
	keys, err := deviceBitmap(descriptor, evKey, keyBitmapBytes)
	if err != nil {
		return openNode{}, false
	}
	axes, err := deviceBitmap(descriptor, evAbs, absBitmapBytes)
	if err != nil {
		return openNode{}, false
	}
	// A node whose name cannot be read still delivers events.
	name, _ := deviceName(descriptor)
	return openNode{path: path, name: name, keys: keys, axes: axes}, true
}

// deviceBitmap reads the capability bitmap of one event type. When the
// kernel writes fewer than length bytes, the rest of the buffer stays
// zero, which reads as codes the node does not declare.
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

// nameRequest builds the EVIOCGNAME ioctl number the way bitmapRequest
// builds EVIOCGBIT. The command is 0x06, and the size in the number is
// the most the kernel writes.
func nameRequest(length int) uintptr {
	return uintptr(2)<<30 | uintptr(length)<<16 | uintptr(0x45)<<8 | uintptr(0x06)
}

// deviceName reads the name of a node from the kernel, such as the
// product name of a controller.
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

// nodeName returns the bytes the kernel wrote, up to the first NUL. A
// name that the kernel cut at the end of the buffer has no NUL.
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
