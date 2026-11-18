package main

// Reading the capabilities of a real evdev node, and the JSON
// snapshot that records them. Each EVIOCG* ioctl returns a bitmap of
// the codes the device supports. The relay copies all of them to the
// virtual device, because a uinput device drops events whose codes it
// did not declare. The snapshot is stored in the bond's Secret so a
// later start can create the virtual device before the controller
// connects.

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"unsafe"
)

// snapshotVersion is the version of the stored document. A reader that
// does not recognize the version creates no virtual device from it,
// and the next connect writes a snapshot in a version it does.
const snapshotVersion = 1

// evdevSnapshot is the value under the evdev key of the bond's
// Secret: the capabilities of every evdev node that one controller
// registers.
type evdevSnapshot struct {
	Version int                 `json:"version"`
	Nodes   []evdevCapabilities `json:"nodes"`
}

// evdevCapabilities is everything needed to create a virtual device
// that mirrors one real evdev node.
//
// Name is the EVIOCGNAME result. It matches a real node to its virtual
// device after a reconnect. Codes is keyed by the kernel's event type
// names (EV_KEY, EV_ABS) so the Secret reads in kernel terms. Axes
// holds the EVIOCGABS result for each EV_ABS code. A virtual axis
// created without a range always reports zero.
type evdevCapabilities struct {
	Name       string              `json:"name"`
	ID         evdevID             `json:"id"`
	Properties []uint16            `json:"properties,omitempty"`
	Codes      map[string][]uint16 `json:"codes,omitempty"`
	Axes       []absAxis           `json:"axes,omitempty"`
}

// evdevID is the bus, vendor, product, and version that EVIOCGID reads
// and UI_DEV_SETUP writes. The virtual device copies them so a program
// that recognizes a gamepad by vendor and product recognizes it.
type evdevID struct {
	Bus     uint16 `json:"bus"`
	Vendor  uint16 `json:"vendor"`
	Product uint16 `json:"product"`
	Version uint16 `json:"version"`
}

// absAxis is the range, fuzz, flat, and resolution that the kernel
// reports for one absolute axis.
type absAxis struct {
	Code       uint16 `json:"code"`
	Minimum    int32  `json:"minimum"`
	Maximum    int32  `json:"maximum"`
	Fuzz       int32  `json:"fuzz"`
	Flat       int32  `json:"flat"`
	Resolution int32  `json:"resolution"`
}

// These structs have the layout of their counterparts in
// linux/input.h. The ioctl request numbers take their size from them.
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

	// inputEvent is struct input_event. The relay copies these bytes
	// from the real node to the virtual one without decoding them, so
	// it uses only the size.
	inputEvent struct {
		Seconds      int64
		Microseconds int64
		Type         uint16
		Code         uint16
		Value        int32
	}
)

// inputEventSize is the size of one struct input_event, 24 bytes on a
// 64-bit kernel. A read from an evdev node returns whole records, and
// uinput takes writes in the same layout.
const inputEventSize = int(unsafe.Sizeof(inputEvent{}))

// The request numbers of the evdev reads. A request that fills a
// buffer encodes the buffer's length in its number.
var eviocgid = ioc(iocRead, uint32(unsafe.Sizeof(inputID{})), evdevLetter, 0x02)

func eviocgname(size uint32) uint32       { return ioc(iocRead, size, evdevLetter, 0x06) }
func eviocgprop(size uint32) uint32       { return ioc(iocRead, size, evdevLetter, 0x09) }
func eviocgbit(event, size uint32) uint32 { return ioc(iocRead, size, evdevLetter, 0x20+event) }

func eviocgabs(axis uint32) uint32 {
	return ioc(iocRead, uint32(unsafe.Sizeof(absInfo{})), evdevLetter, 0x40+axis)
}

// eviocsabs sets the absinfo of one axis. systemd's hwdb uses it to
// apply an EVDEV_ABS_ entry, and this operator uses it to apply the
// axis settings of a claim.
func eviocsabs(axis uint32) uint32 {
	return ioc(iocWrite, uint32(unsafe.Sizeof(absInfo{})), evdevLetter, 0xc0+axis)
}

// eviocsmask sets which events the kernel queues for one open file
// descriptor of an evdev node.
var eviocsmask = ioc(iocWrite, uint32(unsafe.Sizeof(inputMask{})), evdevLetter, 0x93)

// inputMask is struct input_mask, the argument of EVIOCSMASK: an event
// type, the length of the mask in bytes, and a pointer to the mask.
type inputMask struct {
	Type      uint32
	CodesSize uint32
	CodesPtr  uint64
}

// maskLength is the size in bytes of a mask for codes 0 through max.
// The kernel reads a mask in whole longs and returns EINVAL for any
// other length, so the size is rounded up to a multiple of the word
// size.
func maskLength(max int) int {
	const word = int(unsafe.Sizeof(uintptr(0)))
	return (bitmapSize(max) + word - 1) / word * word
}

// evdevNode is a real evdev node that the relay reads. setMasks makes
// the kernel drop the events that no claim wants, so the relay does not
// wake to read them.
type evdevNode struct {
	file *os.File
}

func (n *evdevNode) Read(events []byte) (int, error) { return n.file.Read(events) }
func (n *evdevNode) Close() error                    { return n.file.Close() }

// setMasks sets each mask with EVIOCSMASK. A mask applies only to this
// file descriptor, so other readers of the node still receive every
// event.
func (n *evdevNode) setMasks(masks []eventMask) error {
	return n.control(func(fd int) error {
		for _, mask := range masks {
			if err := setEventMask(fd, mask); err != nil {
				return err
			}
		}
		return nil
	})
}

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
	// The kernel reads the bitmap through a uint64 field that holds its
	// address. The garbage collector does not treat that field as a
	// pointer, so KeepAlive keeps bits reachable until the call returns.
	runtime.KeepAlive(bits)
	if err != nil {
		return fmt.Errorf("limiting event type %d: %w", mask.event, err)
	}
	return nil
}

// axisRange returns the current absinfo of one axis, including any
// values that setAxisRange wrote.
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

// setAxisRange sets the absinfo of one axis. The call writes the whole
// struct, including the current value, so the caller reads it with
// axisRange first and changes only the fields it means to.
func (n *evdevNode) setAxisRange(code uint16, info absInfo) error {
	return n.control(func(fd int) error {
		if err := ioctlPtr(fd, eviocsabs(uint32(code)), unsafe.Pointer(&info)); err != nil {
			return fmt.Errorf("setting axis %d: %w", code, err)
		}
		return nil
	})
}

// control runs call with this node's file descriptor. It uses
// SyscallConn because File.Fd switches the file to blocking mode, and
// then Close cannot end a Read that is in progress.
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

func (linuxInput) open(path string) (realNode, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &evdevNode{file: file}, nil
}

// The event types that the virtual device copies. For each, max is the
// highest code and setBit is the uinput request that declares one code
// on the virtual device.
//
// EV_SYN is absent because every uinput device has it. EV_FF is absent
// because force feedback is written to the real node, and the relay
// copies events in one direction only.
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

// The highest bit of the event type bitmap and of the device property
// bitmap.
const (
	eventTypeMax     = 0x1f
	inputPropertyMax = 0x1f
)

// readCapabilities reads the identity, name, properties, event codes,
// and axis ranges of the evdev node at path.
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

	// uinput limits a device name to UINPUT_MAX_NAME_SIZE bytes, so a
	// longer name could not be copied to the virtual device.
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

// isSet reports whether bit is set in a kernel bitmap.
func isSet(bitmap []byte, bit int) bool {
	return bit/8 < len(bitmap) && bitmap[bit/8]&(1<<(bit%8)) != 0
}

// setCodes returns the codes from 0 through max that are set in a
// kernel bitmap.
func setCodes(bitmap []byte, max int) []uint16 {
	var codes []uint16
	for code := 0; code <= max; code++ {
		if isSet(bitmap, code) {
			codes = append(codes, uint16(code))
		}
	}
	return codes
}

// clen returns the length of the C string at the start of buffer. The
// kernel zero-fills the rest of the buffer, and converting the whole
// buffer to a string would keep those zeros.
func clen(buffer []byte) int {
	if end := bytes.IndexByte(buffer, 0); end >= 0 {
		return end
	}
	return len(buffer)
}
