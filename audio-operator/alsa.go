package main

// This file lists the PCM devices of each sound card and reads the ELD
// block of each HDMI or DisplayPort output. It calls the ALSA control
// ioctls directly, so the operator does not link alsa-lib. cards.go
// reads each card's identity.
//
// The ELD comes from the control element named ELD, not from
// /proc/asound/card<N>/eld#<codec>.<pin>. Both have the same bytes, but
// only the element's identifier has the PCM device number. The second
// number in the proc file name is a pin index, so the proc file does
// not tell you which output a block belongs to.
//
// The structures below mirror the ioctl arguments in
// include/uapi/sound/asound.h. A request number encodes the size of its
// argument, and the kernel rejects a request with the wrong size, so
// alsa_test.go checks every size.

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// sndDir is a variable so that tests can point it at a fixture
// directory.
var sndDir = "/dev/snd"

// pcmNodePattern matches pcmC<card>D<device><p|c>, where p is playback
// and c is capture. The operator publishes a playback PCM as a sink and
// a capture PCM as a source.
var pcmNodePattern = regexp.MustCompile(`^pcmC(\d+)D(\d+)([pc])$`)

// The sizes in bytes of the kernel's control structures on a 64-bit
// kernel. They go into the ioctl request numbers, so they must match
// the kernel exactly.
const (
	ctlElemIDSize    = 64
	ctlElemListSize  = 80
	ctlElemInfoSize  = 272
	ctlElemValueSize = 1224
)

var (
	ctlIoctlElemList = iowr(0x10, ctlElemListSize)
	ctlIoctlElemInfo = iowr(0x11, ctlElemInfoSize)
	ctlIoctlElemRead = iowr(0x12, ctlElemValueSize)
)

// iowr builds an _IOWR request number in the 'U' ioctl group of the
// ALSA control interface.
func iowr(number, size uintptr) uintptr {
	return 3<<30 | size<<16 | 'U'<<8 | number
}

// ctlElemIfacePCM is SNDRV_CTL_ELEM_IFACE_PCM. The ELD element has this
// interface, so the Device field of its identifier is a PCM device
// number.
const ctlElemIfacePCM = 3

// ctlElemTypeBytes is SNDRV_CTL_ELEM_TYPE_BYTES, the type of the ELD
// element.
const ctlElemTypeBytes = 4

// eldElementName is the name that the HDA driver gives the element
// that carries an ELD block.
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
	// IDs points at the caller's array. It is a typed pointer so that the
	// garbage collector keeps the array alive while the kernel writes
	// into it.
	IDs      *ctlElemID
	Reserved [50]byte
}

type ctlElemInfo struct {
	ID     ctlElemID
	Type   int32
	Access uint32
	// Count is the number of values in the element, which for the ELD
	// element is the length of the block. The driver reports zero while
	// it has no valid block, as when no monitor is connected.
	Count    uint32
	Owner    int32
	Value    [128]byte
	Dimen    [8]byte
	Reserved [56]byte
}

// ctlElemValue is the argument of the element read ioctl. The kernel
// declares Value as a union. Its largest member is an array of 128
// longs, which sets the size here, and an element of type bytes uses
// the first maxBytesElement bytes of it.
type ctlElemValue struct {
	ID       ctlElemID
	Indirect uint32
	_        [4]byte
	Value    [1024]byte
	Reserved [128]byte
}

// maxBytesElement is the size of the bytes member of the kernel's value
// union. The read ioctl cannot return a longer element, so
// readBytesElement refuses one. An ELD block is at most ELD_MAX_SIZE,
// 256 bytes, in include/sound/pcm_drm_eld.h, so it always fits.
const maxBytesElement = 512

func (id ctlElemID) name() string { return cText(id.Name[:]) }

// alsaEndpoint is one PCM device of one card in one direction.
//
// HDMI is true when the card has an ELD element for the PCM device, as
// an HDMI or DisplayPort output has and an analog jack does not.
// Monitor is true when that element has a valid block, which means a
// monitor is connected. nameEndpoints sets DeviceName from Identity and
// PCMID.
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

// Name returns the DRA device name, which is empty until nameEndpoints
// sets it.
func (o alsaEndpoint) Name() string { return o.DeviceName }

// Address returns the card and PCM numbers the kernel assigned in this
// boot, for log lines and PipeWire node names. The numbers can change at
// the next boot, so nothing durable uses them as a key.
func (o alsaEndpoint) Address() string { return alsaAddress(o.Card, o.PCM) }

// direction returns whether the endpoint's PipeWire node is a sink or a
// source. It comes from the endpoint and not from the card, because a
// PCM device of a USB card can have a playback endpoint and a capture
// endpoint, each with its own node.
func (o alsaEndpoint) direction() pwDirection {
	if o.Capture {
		return directionSource
	}
	return directionSink
}

// graphAddress returns the key of the endpoint's node in the index of
// the PipeWire graph.
func (o alsaEndpoint) graphAddress() nodeAddress {
	return nodeAddress{pcmAddress: pcmAddress{Card: o.Card, PCM: o.PCM}, Direction: o.direction()}
}

// connectionType returns the connectionType attribute of the endpoint.
// An HDMI or DisplayPort output reads it from its ELD block, and with no
// valid block it returns "", because only the block tells HDMI from
// DisplayPort. Every endpoint of a USB card returns usb, and every other
// endpoint is an analog jack.
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

type pcmDevice struct {
	PCM     int
	Capture bool
}

// readEndpoints lists every playback and capture PCM device in sndDir,
// reads the ELD element of each playback device that has one, and
// reads the identity of each card.
//
// The list depends only on the cards, so a monitor that is plugged in
// or a PipeWire node that appears or disappears does not add or remove
// a published device.
func readEndpoints() ([]alsaEndpoint, error) {
	entries, err := os.ReadDir(sndDir)
	if errors.Is(err, os.ErrNotExist) {
		// A Kubernetes node with no sound card has no /dev/snd. Returning
		// no endpoints and no error lets the DaemonSet run the pod on
		// every node, and the pod idles where there is no card. Any other
		// read error fails.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing the sound devices: %w", err)
	}

	var outputs []alsaEndpoint
	byCard := map[int][]pcmDevice{}
	for _, entry := range entries {
		match := pcmNodePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		card, _ := strconv.Atoi(match[1])
		// The PCM number must fit the 32-bit device field of the kernel's
		// snd_pcm_info. A node whose number does not fit is skipped, like
		// a node whose name does not match.
		pcm, err := strconv.ParseInt(match[2], 10, 32)
		if err != nil {
			continue
		}
		byCard[card] = append(byCard[card], pcmDevice{PCM: int(pcm), Capture: match[3] == "c"})
	}

	for card, devices := range byCard {
		blocks, err := readELDElements(card)
		if err != nil {
			// A card whose control node cannot be read still plays sound,
			// so its endpoints are published without ELD data, as analog
			// or USB endpoints.
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
			// An ELD element belongs to the playback PCM, so the capture
			// PCM with the same number gets no monitor.
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

// readELDElements returns the parsed ELD block of each PCM device of a
// card that has an ELD element, keyed by PCM device number. The value
// is nil when the driver has no valid block, as for a pin with no
// monitor, and when the block cannot be read or parsed.
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

// listControlElements returns the identifiers of all control elements
// of a card. The first ioctl call has no buffer and returns the count.
// The second call reads that many identifiers into a buffer.
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

// readBytesElement returns the value of a bytes element. It reads the
// element's info first to get the current length, because the length of
// an ELD element changes with the monitor.
func readBytesElement(control *os.File, id ctlElemID) ([]byte, error) {
	info := ctlElemInfo{ID: id}
	if err := ioctl(control, ctlIoctlElemInfo, unsafe.Pointer(&info)); err != nil {
		return nil, fmt.Errorf("reading the element information: %w", err)
	}
	if info.Type != ctlElemTypeBytes {
		return nil, fmt.Errorf("the element has type %d, and this function reads only type %d", info.Type, ctlElemTypeBytes)
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

func ioctl(file *os.File, request uintptr, argument unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), request, uintptr(argument))
	if errno != 0 {
		return errno
	}
	return nil
}
