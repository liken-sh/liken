package main

// What a real evdev node can do, and how this operator writes that
// down. The kernel answers each EVIOCG* ioctl with a bitmap of the
// codes a device sets, and the relay needs all of them: a virtual
// device that declares fewer codes than the real one drops the events
// it did not declare. The snapshot is JSON because it is stored in the
// bond's Secret and read back on a later start, before the controller
// has connected.

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// snapshotVersion is the version of the stored document. A reader that
// does not recognize the version creates nothing from it, and the next
// connect writes a document this operator does understand.
const snapshotVersion = 1

// evdevSnapshot is what the bond's Secret holds under the evdev key:
// every evdev node one controller registers, with the capabilities the
// virtual device must mirror.
type evdevSnapshot struct {
	Version int                 `json:"version"`
	Nodes   []evdevCapabilities `json:"nodes"`
}

// evdevCapabilities is one real evdev node, as the virtual device that
// stands in for it needs to be built.
//
// Name is EVIOCGNAME, and it is the match between a real node and its
// virtual device across a reconnect. Codes is keyed by the kernel's own
// event-type name, so a person reading the Secret reads kernel
// vocabulary. Axes carries EVIOCGABS for each ABS code, because a stick
// with no range reports every position as zero.
type evdevCapabilities struct {
	Name       string              `json:"name"`
	ID         evdevID             `json:"id"`
	Properties []uint16            `json:"properties,omitempty"`
	Codes      map[string][]uint16 `json:"codes,omitempty"`
	Axes       []absAxis           `json:"axes,omitempty"`
}

// evdevID is the device's bus, vendor, product, and version, which
// EVIOCGID reads and UI_DEV_SETUP writes. A consumer that matches a
// gamepad by its vendor and product finds the virtual device.
type evdevID struct {
	Bus     uint16 `json:"bus"`
	Vendor  uint16 `json:"vendor"`
	Product uint16 `json:"product"`
	Version uint16 `json:"version"`
}

// absAxis is one absolute axis with the range and the noise filters
// the kernel reports for it.
type absAxis struct {
	Code       uint16 `json:"code"`
	Minimum    int32  `json:"minimum"`
	Maximum    int32  `json:"maximum"`
	Fuzz       int32  `json:"fuzz"`
	Flat       int32  `json:"flat"`
	Resolution int32  `json:"resolution"`
}

// The kernel structures the input layer carries. Each one has the
// layout of its counterpart in linux/input.h, and the request numbers
// take their sizes from these declarations rather than from a literal.
type (
	inputID struct {
		Bus     uint16
		Vendor  uint16
		Product uint16
		Version uint16
	}

	absInfo struct {
		Value      int32
		Minimum    int32
		Maximum    int32
		Fuzz       int32
		Flat       int32
		Resolution int32
	}

	// inputEvent is one event on the wire between a real node and a
	// virtual one. The relay moves these bytes without reading them,
	// and the only fact it needs is the size of one record.
	inputEvent struct {
		Seconds      int64
		Microseconds int64
		Type         uint16
		Code         uint16
		Value        int32
	}
)

// inputEventSize is one struct input_event, which is 24 bytes on a
// 64-bit kernel. A read from an evdev node answers with whole records,
// and a write to uinput takes them in the same layout.
const inputEventSize = int(unsafe.Sizeof(inputEvent{}))

// The evdev reads. Each one answers with a fixed structure or fills a
// buffer whose length is part of the request number.
var eviocgid = ioc(iocRead, uint32(unsafe.Sizeof(inputID{})), evdevLetter, 0x02)

func eviocgname(size uint32) uint32       { return ioc(iocRead, size, evdevLetter, 0x06) }
func eviocgprop(size uint32) uint32       { return ioc(iocRead, size, evdevLetter, 0x09) }
func eviocgbit(event, size uint32) uint32 { return ioc(iocRead, size, evdevLetter, 0x20+event) }

func eviocgabs(axis uint32) uint32 {
	return ioc(iocRead, uint32(unsafe.Sizeof(absInfo{})), evdevLetter, 0x40+axis)
}

// eviocsabs writes one axis's absinfo back to the device. It is how
// systemd's hwdb applies an EVDEV_ABS_ entry, and how this driver
// applies a claim's axes.
func eviocsabs(axis uint32) uint32 {
	return ioc(iocWrite, uint32(unsafe.Sizeof(absInfo{})), evdevLetter, 0xc0+axis)
}

// eviocsmask limits what the kernel queues on one open node. It is
// the one evdev call this operator makes that writes.
var eviocsmask = ioc(iocWrite, uint32(unsafe.Sizeof(inputMask{})), evdevLetter, 0x93)

// inputMask is struct input_mask, the argument EVIOCSMASK takes: the
// event type the mask limits, the length of the mask in bytes, and
// the address of the mask itself.
type inputMask struct {
	Type      uint32
	CodesSize uint32
	CodesPtr  uint64
}

// maskLength is how many bytes a mask over the codes up to max takes.
// The kernel reads a mask in whole longs and answers EINVAL for any
// other length, so the bitmap is rounded up to a whole number of
// them.
func maskLength(max int) int {
	const word = int(unsafe.Sizeof(uintptr(0)))
	return (bitmapSize(max) + word - 1) / word * word
}

// evdevNode is one real evdev node the relay reads. The type carries
// the narrowing, because a node the kernel filters is the whole
// mechanism by which a class nobody claimed costs nothing.
type evdevNode struct {
	file *os.File
}

func (n *evdevNode) Read(events []byte) (int, error) { return n.file.Read(events) }
func (n *evdevNode) Close() error                    { return n.file.Close() }

// narrow tells the kernel which events to queue on this fd. The mask
// for event type EV_SYN is the mask of event types, and EV_SYN itself
// is never filtered, so a node narrowed to nothing still reports the
// end of a frame and nothing inside it.
func (n *evdevNode) narrow(masks []eventMask) error {
	return n.control(func(fd int) error {
		for _, mask := range masks {
			if err := setEventMask(fd, mask); err != nil {
				return err
			}
		}
		return nil
	})
}

// setEventMask makes one EVIOCSMASK call.
func setEventMask(fd int, mask eventMask) error {
	bits := make([]byte, maskLength(mask.max))
	for _, code := range mask.codes {
		if int(code) <= mask.max {
			bits[code/8] |= 1 << (code % 8)
		}
	}
	argument := inputMask{
		Type:      mask.event,
		CodesSize: uint32(len(bits)),
		CodesPtr:  uint64(uintptr(unsafe.Pointer(&bits[0]))),
	}
	err := ioctlPtr(fd, eviocsmask, unsafe.Pointer(&argument))
	// The kernel reads the mask through an address this program put in
	// a field, which the garbage collector does not follow, so the
	// bitmap is held until the call has returned.
	runtime.KeepAlive(bits)
	if err != nil {
		return fmt.Errorf("limiting event type %d: %w", mask.event, err)
	}
	return nil
}

// axisRange reads one axis's absinfo as the kernel reports it now,
// which is what this operator last wrote when it has written one.
func (n *evdevNode) axisRange(code uint16) (absInfo, error) {
	var info absInfo
	err := n.control(func(fd int) error {
		if err := ioctlPtr(fd, eviocgabs(uint32(code)), unsafe.Pointer(&info)); err != nil {
			return fmt.Errorf("reading axis %d: %w", code, err)
		}
		return nil
	})
	return info, err
}

// setAxisRange writes one axis's absinfo. The whole structure is
// written, the current position included, so the caller reads it
// first and changes only the fields it means to change.
func (n *evdevNode) setAxisRange(code uint16, info absInfo) error {
	return n.control(func(fd int) error {
		if err := ioctlPtr(fd, eviocsabs(uint32(code)), unsafe.Pointer(&info)); err != nil {
			return fmt.Errorf("setting axis %d: %w", code, err)
		}
		return nil
	})
}

// control runs one call against this node's file descriptor. The fd
// is reached through the runtime's own accessor rather than through
// Fd, which would take the file out of the poller and leave a Close
// unable to end the read in flight on it.
func (n *evdevNode) control(call func(fd int) error) error {
	connection, err := n.file.SyscallConn()
	if err != nil {
		return err
	}
	var failed error
	if err := connection.Control(func(fd uintptr) { failed = call(int(fd)) }); err != nil {
		return err
	}
	return failed
}

// open opens a real evdev node for reading.
func (linuxInput) open(path string) (realNode, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &evdevNode{file: file}, nil
}

// The event types this operator mirrors, with the code limit and the
// request that declares one code of that type on a virtual device.
//
// EV_SYN is not here because every uinput device has it from the
// moment the kernel allocates it. EV_FF is not here because force
// feedback is a write into the real node, and the relay carries events
// one way.
var mirroredTypes = []struct {
	name   string
	event  uint32
	max    int
	setBit uint32
}{
	{name: "EV_KEY", event: 0x01, max: 0x2ff, setBit: uiSetKeyBit},
	{name: "EV_REL", event: 0x02, max: 0x0f, setBit: uiSetRelBit},
	{name: "EV_ABS", event: 0x03, max: 0x3f, setBit: uiSetAbsBit},
	{name: "EV_MSC", event: 0x04, max: 0x07, setBit: uiSetMscBit},
	{name: "EV_SW", event: 0x05, max: 0x10, setBit: uiSetSwBit},
	{name: "EV_LED", event: 0x11, max: 0x0f, setBit: uiSetLedBit},
}

// The limits of the two bitmaps that are not one of the mirrored
// types: the event types themselves, and the device properties.
const (
	eventTypeMax     = 0x1f
	inputPropertyMax = 0x1f
)

// readCapabilities reads everything a virtual device needs to stand in
// for one real evdev node.
func (linuxInput) readCapabilities(path string) (evdevCapabilities, error) {
	file, err := os.Open(path)
	if err != nil {
		return evdevCapabilities{}, err
	}
	defer file.Close()
	fd := int(file.Fd())

	caps := evdevCapabilities{Codes: map[string][]uint16{}}
	var id inputID
	if err := ioctlPtr(fd, eviocgid, unsafe.Pointer(&id)); err != nil {
		return evdevCapabilities{}, fmt.Errorf("reading the device id: %w", err)
	}
	caps.ID = evdevID{Bus: id.Bus, Vendor: id.Vendor, Product: id.Product, Version: id.Version}

	// The name buffer is UINPUT_MAX_NAME_SIZE, because a name longer
	// than that does not fit the virtual device this snapshot creates.
	name := make([]byte, uinputMaxNameSize)
	if err := ioctlPtr(fd, eviocgname(uint32(len(name))), unsafe.Pointer(&name[0])); err != nil {
		return evdevCapabilities{}, fmt.Errorf("reading the device name: %w", err)
	}
	caps.Name = string(name[:clen(name)])

	properties := make([]byte, bitmapSize(inputPropertyMax))
	if err := ioctlPtr(fd, eviocgprop(uint32(len(properties))), unsafe.Pointer(&properties[0])); err != nil {
		return evdevCapabilities{}, fmt.Errorf("reading the device properties: %w", err)
	}
	caps.Properties = setCodes(properties, inputPropertyMax)

	events := make([]byte, bitmapSize(eventTypeMax))
	if err := ioctlPtr(fd, eviocgbit(0, uint32(len(events))), unsafe.Pointer(&events[0])); err != nil {
		return evdevCapabilities{}, fmt.Errorf("reading the event types: %w", err)
	}
	for _, mirrored := range mirroredTypes {
		if !isSet(events, int(mirrored.event)) {
			continue
		}
		codes := make([]byte, bitmapSize(mirrored.max))
		if err := ioctlPtr(fd, eviocgbit(mirrored.event, uint32(len(codes))), unsafe.Pointer(&codes[0])); err != nil {
			return evdevCapabilities{}, fmt.Errorf("reading the %s codes: %w", mirrored.name, err)
		}
		set := setCodes(codes, mirrored.max)
		if len(set) == 0 {
			continue
		}
		caps.Codes[mirrored.name] = set
	}

	for _, axis := range caps.Codes["EV_ABS"] {
		var info absInfo
		if err := ioctlPtr(fd, eviocgabs(uint32(axis)), unsafe.Pointer(&info)); err != nil {
			return evdevCapabilities{}, fmt.Errorf("reading axis %d: %w", axis, err)
		}
		caps.Axes = append(caps.Axes, absAxis{
			Code:       axis,
			Minimum:    info.Minimum,
			Maximum:    info.Maximum,
			Fuzz:       info.Fuzz,
			Flat:       info.Flat,
			Resolution: info.Resolution,
		})
	}
	return caps, nil
}

// bitmapSize is how many bytes hold the bits from zero to max.
func bitmapSize(max int) int { return max/8 + 1 }

// isSet reports whether one bit is set in a kernel bitmap.
func isSet(bitmap []byte, bit int) bool {
	return bit/8 < len(bitmap) && bitmap[bit/8]&(1<<(bit%8)) != 0
}

// setCodes lists the bits a kernel bitmap has set, which is how every
// capability read answers.
func setCodes(bitmap []byte, max int) []uint16 {
	var codes []uint16
	for code := 0; code <= max; code++ {
		if isSet(bitmap, code) {
			codes = append(codes, uint16(code))
		}
	}
	return codes
}

// clen is the length of a C string in a fixed buffer. The kernel fills
// the rest of the buffer with zeroes, and a Go string of the whole
// buffer would carry them.
func clen(buffer []byte) int {
	if end := bytes.IndexByte(buffer, 0); end >= 0 {
		return end
	}
	return len(buffer)
}
