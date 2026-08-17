package main

// Reading ALSA through the control interface, without ALSA's own
// library.
//
// The operator needs two facts from the card: which PCM devices it
// plays through, and the ELD block for each one that drives an HDMI
// or a DisplayPort output. Both come from the nodes the raw claim
// already delivers, so this file opens /dev/snd and nothing else.
//
// The ELD comes from the control element named ELD, and not from
// /proc/asound/card<N>/eld#<codec>.<pin>. The two carry the same
// bytes. The control element carries the PCM device number with them,
// in the element's own identifier, and the proc file's second number
// is a pin index instead. The pin index is not the PCM device, so a
// proc file alone cannot say which output a block describes.
//
// The kernel's control interface is three ioctls on
// /dev/snd/controlC<N>. Their argument structures are in
// include/uapi/sound/asound.h, and the Go structures below mirror
// them field for field. controls_test.go asserts every size, because
// a size that disagrees with the kernel's builds an ioctl number the
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

// playbackPCMPattern matches an ALSA playback PCM node,
// pcmC<card>D<device>p. The trailing letter is the stream direction,
// p for playback and c for capture, so the pattern is what keeps a
// microphone out of a list of outputs.
var playbackPCMPattern = regexp.MustCompile(`^pcmC(\d+)D(\d+)p$`)

// The control interface's structure sizes, in bytes, on a 64-bit
// kernel. The ioctl number carries the size of its argument, so these
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
// element identifier carries the PCM device number, which is the
// whole reason this operator reads elements instead of proc files.
const ctlElemIfacePCM = 3

// ctlElemTypeBytes is the element type of a block of bytes. The ELD
// element is one, and its value count is the block's length.
const ctlElemTypeBytes = 4

// eldElementName is the name the HDA driver gives the element that
// carries an ELD block.
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

// ctlElemValue carries one element's value. The value field is a
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
// longer than that carries a pointer instead, which this operator
// does not read. An ELD block is at most 256 bytes, so no block
// reaches the limit.
const maxBytesElement = 512

// name reads an element's name out of its fixed-length field.
func (id ctlElemID) name() string {
	for i, c := range id.Name {
		if c == 0 {
			return string(id.Name[:i])
		}
	}
	return string(id.Name[:])
}

// alsaOutput is one physical output of one card: one playback PCM
// device, and what the card says about the monitor behind it.
//
// HDMI reports whether the PCM device has an ELD element. An output
// that has one is an HDMI or a DisplayPort output, and an output that
// has none is the analog jack. Monitor reports whether that element
// currently holds a block this operator could parse. That tells a
// connected monitor from an HDMI cable with no monitor on it.
type alsaOutput struct {
	Card    int
	PCM     int
	HDMI    bool
	Monitor bool
	ELD     eld
}

// Name is the DRA device name this output publishes under.
func (o alsaOutput) Name() string { return deviceName(o.Card, o.PCM) }

// connectionType is what the device's attribute carries. An output
// with no ELD element is the analog jack. An output whose ELD block
// is absent or unreadable publishes no connection type, because the
// operator cannot tell an HDMI cable from a DisplayPort one without
// the block.
func (o alsaOutput) connectionType() string {
	if !o.HDMI {
		return "analog"
	}
	if !o.Monitor {
		return ""
	}
	return o.ELD.ConnectionType
}

// readOutputs enumerates every playback PCM the delivered nodes hold,
// and reads the ELD element of each one that has one.
//
// The membership of this list does not depend on PipeWire. A card's
// playback PCM devices are the physical outputs it has, whether a
// monitor is connected to one or not and whether a sound server holds
// one or not, so the published inventory stays still while monitors
// and sinks come and go.
func readOutputs() ([]alsaOutput, error) {
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

	var outputs []alsaOutput
	byCard := map[int][]int{}
	for _, entry := range entries {
		match := playbackPCMPattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		card, _ := strconv.Atoi(match[1])
		pcm, _ := strconv.Atoi(match[2])
		byCard[card] = append(byCard[card], pcm)
	}

	for card, pcms := range byCard {
		blocks, err := readELDElements(card)
		if err != nil {
			// A card whose control node this operator cannot read still
			// has outputs, and they publish as analog. Reporting the
			// failure and continuing is better than publishing nothing,
			// because the sinks still work.
			fmt.Fprintf(os.Stderr, "reading the ELD elements of card %d: %v\n", card, err)
		}
		for _, pcm := range pcms {
			output := alsaOutput{Card: card, PCM: pcm}
			block, hasElement := blocks[pcm]
			output.HDMI = hasElement
			if block != nil {
				output.Monitor = true
				output.ELD = *block
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
// carries the current length, and an ELD element's length changes
// with the monitor.
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
		return nil, fmt.Errorf("the element holds %d bytes, and a bytes element carries %d inline",
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
