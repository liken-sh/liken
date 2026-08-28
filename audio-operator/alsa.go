package main

// Reading ALSA through the control interface, without ALSA's own
// library.
//
// The operator needs three facts from the card: which PCM devices
// it plays through, which it records through, and the ELD block for
// each one that drives an HDMI or a DisplayPort output. All three
// come from the nodes the raw claim delivers, so this file opens
// /dev/snd and nothing else. What the card is, and where it is on the
// machine, is cards.go.
//
// The ELD comes from the control element named ELD, and not from
// /proc/asound/card<N>/eld#<codec>.<pin>. The two hold the same
// bytes. The control element holds the PCM device number with them,
// in the element's own identifier, and the proc file's second number
// is a pin index instead. The pin index is not the PCM device, so a
// proc file alone cannot say which output a block describes.
//
// The kernel's control interface is three ioctls on
// /dev/snd/controlC<N>. Their argument structures are in
// include/uapi/sound/asound.h, and the Go structures below mirror
// them field for field. alsa_test.go asserts every size, because a
// size that disagrees with the kernel's builds an ioctl number the
// kernel does not answer.

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sndDir is where the ALSA device nodes appear. It is a variable so
// the tests can point it at a directory they control.
var sndDir = "/dev/snd"

// pcmNodePattern matches an ALSA PCM node, pcmC<card>D<device><p|c>.
// The trailing letter is the stream direction, p for playback and c
// for capture, and the operator publishes both: a playback PCM is a
// sink and a capture PCM is a source.
var pcmNodePattern = regexp.MustCompile(`^pcmC(\d+)D(\d+)([pc])$`)

// The control interface's structure sizes, in bytes, on a 64-bit
// kernel. The ioctl number encodes the size of its argument, so these
// are part of the protocol and not an implementation detail.
const (
	ctlElemIDSize    = 64
	ctlElemListSize  = 80
	ctlElemInfoSize  = 272
	ctlElemValueSize = 1224
)

// The three ioctls this operator calls, on the 'U' ioctl group that
// the ALSA control interface owns. Each one reads and writes its
// argument, which is what _IOWR means and what the two high bits say.
var (
	ctlIoctlElemList = iowr(0x10, ctlElemListSize)
	ctlIoctlElemInfo = iowr(0x11, ctlElemInfoSize)
	ctlIoctlElemRead = iowr(0x12, ctlElemValueSize)
)

// iowr builds an ioctl request number for the ALSA control interface:
// the read and write direction bits, the argument size, the 'U'
// group, and the command number.
func iowr(number, size uintptr) uintptr {
	return 3<<30 | size<<16 | 'U'<<8 | number
}

// ctlElemIfacePCM is the interface a control element belongs to. The
// ELD element is registered against the PCM interface, and its
// element identifier holds the PCM device number, which is the whole
// reason this operator reads elements instead of proc files.
const ctlElemIfacePCM = 3

// ctlElemTypeBytes is the element type of a block of bytes. The ELD
// element is one, and its value count is the block's length.
const ctlElemTypeBytes = 4

// eldElementName is the name the HDA driver gives the element that
// holds an ELD block.
const eldElementName = "ELD"

type ctlElemID struct {
	NumID     uint32
	Interface int32
	Device    uint32
	Subdevice uint32
	Name      [44]byte
	Index     uint32
}

type ctlElemList struct {
	Offset uint32
	Space  uint32
	Used   uint32
	Count  uint32
	// IDs points at the caller's array. It is a typed pointer, so the
	// garbage collector sees the array through it while the kernel
	// writes into it.
	IDs      *ctlElemID
	Reserved [50]byte
}

type ctlElemInfo struct {
	ID     ctlElemID
	Type   int32
	Access uint32
	// Count is the number of values the element holds. For the ELD
	// element it is the length of the block, and the driver reports
	// zero when the block is invalid, which is what an unplugged
	// monitor looks like from here.
	Count    uint32
	Owner    int32
	Value    [128]byte
	Dimen    [8]byte
	Reserved [56]byte
}

// ctlElemValue holds one element's value. The value field is a
// union in the kernel's header, and its largest member is an array of
// 128 longs, which is what sets the field's size here. A bytes
// element reads the first maxBytesElement of it.
type ctlElemValue struct {
	ID       ctlElemID
	Indirect uint32
	_        [4]byte
	Value    [1024]byte
	Reserved [128]byte
}

// maxBytesElement is how many bytes one element of type bytes can
// hold inline. The union's bytes member is 512 bytes, and an element
// longer than that holds a pointer instead, which this operator does
// not read. An ELD block is at most 256 bytes, so no block reaches
// the limit.
const maxBytesElement = 512

// name reads an element's name out of its fixed-length field.
func (id ctlElemID) name() string { return cText(id.Name[:]) }

// alsaEndpoint is one endpoint of one card: one PCM device in one
// direction, what the card says about the monitor behind it, and the
// identity its device name is built from.
//
// Capture says the PCM device records instead of plays. HDMI says
// the card declares an ELD element for the device, which is what an
// HDMI or DisplayPort output has and the analog jack does not, and
// Monitor says the block read and a monitor answers. Identity and
// PCMID are the parts endpointName builds the device name from, and
// DeviceName is what nameEndpoints stamped, empty until it did.
type alsaEndpoint struct {
	Card       int
	PCM        int
	Capture    bool
	HDMI       bool
	Monitor    bool
	ELD        eld
	Identity   cardIdentity
	PCMID      string
	DeviceName string
}

// Name is the DRA device name this endpoint publishes under, and it
// is empty until nameEndpoints stamps it.
func (o alsaEndpoint) Name() string { return o.DeviceName }

// Address is the card and device number the kernel assigned this
// boot. It names the PipeWire node and it reads well in a log line,
// and nothing durable is keyed to it.
func (o alsaEndpoint) Address() string { return alsaAddress(o.Card, o.PCM) }

// direction is the half of the graph this endpoint's node is in. It
// is the endpoint's own and not the card's: one PCM device of a USB
// card carries a playback endpoint and a capture endpoint, and each
// one has a node of its own.
func (o alsaEndpoint) direction() pwDirection {
	if o.Capture {
		return directionSource
	}
	return directionSink
}

// graphAddress is where this endpoint's node is in PipeWire's graph:
// the card and PCM numbers and the direction together name one node,
// and the graph read builds its index on that key.
func (o alsaEndpoint) graphAddress() nodeAddress {
	return nodeAddress{pcmAddress: pcmAddress{Card: o.Card, PCM: o.PCM}, Direction: o.direction()}
}

// connectionType is the value of the device's attribute. An
// endpoint with an ELD element is an HDMI or DisplayPort output, and
// one whose block is absent or unreadable publishes no connection
// type, because the operator cannot tell an HDMI cable from a
// DisplayPort one without the block. A card on the USB bus publishes
// usb whichever direction the endpoint runs in, and every other
// endpoint is the analog jack.
func (o alsaEndpoint) connectionType() string {
	if o.HDMI {
		if !o.Monitor {
			return ""
		}
		return o.ELD.ConnectionType
	}
	if o.Identity.Bus == usbBus {
		return usbBus
	}
	return "analog"
}

// pcmDevice is one PCM device of one card and the direction it runs
// in.
type pcmDevice struct {
	PCM     int
	Capture bool
}

// readEndpoints enumerates every PCM device the delivered nodes hold in
// both directions, reads the ELD element of each playback one that
// has one, and reads what each card says about itself.
//
// The membership of this list does not depend on PipeWire. A card's
// PCM devices are the physical endpoints it has, whether a monitor is
// connected to one or not and whether a sound server holds one or
// not, so the published inventory does not change while monitors and
// sinks come and go.
func readEndpoints() ([]alsaEndpoint, error) {
	entries, err := os.ReadDir(sndDir)
	if errors.Is(err, os.ErrNotExist) {
		// A node with no sound card has no /dev/snd for the claim to
		// deliver. The operator publishes no output and keeps serving, so
		// a DaemonSet can place a pod on every node and the pod idles
		// where there is nothing to play. A read error on a directory
		// that does exist still fails, because that is a delivered card
		// the operator cannot enumerate.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sndDir, err)
	}

	var outputs []alsaEndpoint
	byCard := map[int][]pcmDevice{}
	for _, entry := range entries {
		match := pcmNodePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		card, _ := strconv.Atoi(match[1])
		pcm, _ := strconv.Atoi(match[2])
		byCard[card] = append(byCard[card], pcmDevice{PCM: pcm, Capture: match[3] == "c"})
	}

	for card, devices := range byCard {
		blocks, err := readELDElements(card)
		if err != nil {
			// A card whose control node this operator cannot read still
			// has outputs, and they publish as analog. Reporting the
			// failure and continuing is better than publishing nothing,
			// because the sinks still work.
			fmt.Fprintf(os.Stderr, "reading the ELD elements of card %d: %v\n", card, err)
		}
		identity := readCardIdentity(card)
		for _, device := range devices {
			output := alsaEndpoint{
				Card:     card,
				PCM:      device.PCM,
				Capture:  device.Capture,
				Identity: identity,
				PCMID:    readPCMID(card, device.PCM, device.Capture),
			}
			// An ELD element belongs to a playback PCM. A capture PCM of
			// the same device number is a different endpoint, and the
			// monitor behind the speakers says nothing about it.
			if block, hasElement := blocks[device.PCM]; hasElement && !device.Capture {
				output.HDMI = true
				if block != nil {
					output.Monitor = true
					output.ELD = *block
				}
			}
			outputs = append(outputs, output)
		}
	}
	return outputs, nil
}

// readELDElements reads every ELD element of one card. The result
// holds one entry for each PCM device that has an element, and the
// value is nil when the element holds no valid block, which is what
// the driver reports for a pin with no monitor on it.
func readELDElements(card int) (map[int]*eld, error) {
	control, err := os.Open(fmt.Sprintf("%s/controlC%d", sndDir, card))
	if err != nil {
		return nil, err
	}
	defer control.Close()

	ids, err := listControlElements(control)
	if err != nil {
		return nil, err
	}

	blocks := map[int]*eld{}
	for _, id := range ids {
		if id.Interface != ctlElemIfacePCM || id.name() != eldElementName {
			continue
		}
		device := int(id.Device)
		blocks[device] = nil

		raw, err := readBytesElement(control, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the ELD of card %d device %d: %v\n", card, device, err)
			continue
		}
		if len(raw) == 0 {
			// The driver reports a zero length when the block is
			// invalid. The monitor is gone, or it never answered.
			continue
		}
		block, err := parseELD(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parsing the ELD of card %d device %d: %v\n", card, device, err)
			continue
		}
		blocks[device] = &block
	}
	return blocks, nil
}

// listControlElements reads the card's whole element list. The first
// call asks for the count, and the second reads that many
// identifiers, which is the protocol the ioctl defines.
func listControlElements(control *os.File) ([]ctlElemID, error) {
	var list ctlElemList
	if err := ioctl(control, ctlIoctlElemList, unsafe.Pointer(&list)); err != nil {
		return nil, fmt.Errorf("counting the control elements: %w", err)
	}
	if list.Count == 0 {
		return nil, nil
	}

	ids := make([]ctlElemID, list.Count)
	list.Offset = 0
	list.Space = list.Count
	list.IDs = &ids[0]
	if err := ioctl(control, ctlIoctlElemList, unsafe.Pointer(&list)); err != nil {
		return nil, fmt.Errorf("listing the control elements: %w", err)
	}
	return ids[:list.Used], nil
}

// readBytesElement reads the value of one element of type bytes. It
// asks for the element's information first, because the information
// holds the current length, and an ELD element's length changes with
// the monitor.
func readBytesElement(control *os.File, id ctlElemID) ([]byte, error) {
	info := ctlElemInfo{ID: id}
	if err := ioctl(control, ctlIoctlElemInfo, unsafe.Pointer(&info)); err != nil {
		return nil, fmt.Errorf("reading the element information: %w", err)
	}
	if info.Type != ctlElemTypeBytes {
		return nil, fmt.Errorf("the element holds type %d, and this reads type %d", info.Type, ctlElemTypeBytes)
	}
	if info.Count == 0 {
		return nil, nil
	}
	if info.Count > maxBytesElement {
		return nil, fmt.Errorf("the element holds %d bytes, and a bytes element holds %d inline",
			info.Count, maxBytesElement)
	}

	value := ctlElemValue{ID: id}
	if err := ioctl(control, ctlIoctlElemRead, unsafe.Pointer(&value)); err != nil {
		return nil, fmt.Errorf("reading the element value: %w", err)
	}
	raw := make([]byte, info.Count)
	copy(raw, value.Value[:info.Count])
	return raw, nil
}

// ioctl sends one request on an open file. The pointer is to a
// structure the kernel reads and writes in place.
func ioctl(file *os.File, request uintptr, argument unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), request, uintptr(argument))
	if errno != 0 {
		return errno
	}
	return nil
}
