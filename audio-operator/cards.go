package main

// Where a card's identity comes from.
//
// Three sources hold the parts of it. The control device answers
// with the card's own id, driver, and name. sysfs says where the
// card is: the PCI function or the USB port it is on, and for a USB
// card the vendor, product, and serial of the device above the
// interface. And the control device answers with the driver's own
// name for each PCM device, the same name /proc/asound prints, which
// the container runtime masks. liken runs no udev, so the operator reads the same
// attributes that 60-persistent-alsa.rules reads to build
// /dev/snd/by-id and by-path, and names.go builds the same names.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// sysDir and procDir are the two pseudo-filesystems this file reads.
// They are variables so the tests can point them at fixtures that
// mirror a real machine's trees.
var (
	sysDir  = "/sys"
	procDir = "/proc"
)

// ctlCardInfoSize is the size of struct snd_ctl_card_info in bytes.
// The ioctl number encodes it, so it is part of the protocol.
const ctlCardInfoSize = 376

// ctlIoctlCardInfo reads the card's own identity. It is _IOR and not
// _IOWR: the caller passes an empty structure and the kernel fills
// every field.
var ctlIoctlCardInfo = ior(0x01, ctlCardInfoSize)

// ior builds an ioctl request number that only reads: the read
// direction bit, the argument size, the 'U' group, and the command
// number.
func ior(number, size uintptr) uintptr {
	return 2<<30 | size<<16 | 'U'<<8 | number
}

// ctlCardInfo mirrors struct snd_ctl_card_info field for field. The
// two fields this operator does not read are here because the layout
// is the protocol: dropping one would move every field after it.
type ctlCardInfo struct {
	Card int32
	// Pad is the kernel's own name for the word after the number,
	// which it reserves and never fills.
	Pad        int32
	ID         [16]byte
	Driver     [16]byte
	Name       [32]byte
	LongName   [80]byte
	Reserved   [16]byte
	MixerName  [80]byte
	Components [128]byte
}

// cardIdentity is what one card says about itself. ID, Driver, and
// Name come from the control device, and Bus, Location, Vendor,
// Product, and Serial come from sysfs. A device name is built from
// the sysfs half, and the resource status reports both.
type cardIdentity struct {
	ID       string
	Driver   string
	Name     string
	Bus      string
	Location string
	Vendor   string
	Product  string
	Serial   string
}

// The two buses this operator names a card on. A card on any other
// bus has no name form, and the reason reaches the operator's log.
const (
	pciBus = "pci"
	usbBus = "usb"
)

// readCardIdentity reads everything one card says about itself. A
// field it cannot read is empty, and no error is raised here: the
// name builder is what refuses an endpoint whose identity is too
// thin to name, and it says which part was missing.
func readCardIdentity(card int) cardIdentity {
	identity := cardIdentity{}
	if info, err := readCardInfo(card); err == nil {
		identity.ID = cText(info.ID[:])
		identity.Driver = cText(info.Driver[:])
		identity.Name = cText(info.Name[:])
	}
	readCardPlace(card, &identity)
	return identity
}

// readCardInfo asks the card's control device who it is.
func readCardInfo(card int) (ctlCardInfo, error) {
	var info ctlCardInfo
	control, err := os.Open(fmt.Sprintf("%s/controlC%d", sndDir, card))
	if err != nil {
		return info, err
	}
	defer control.Close()
	if err := ioctl(control, ctlIoctlCardInfo, unsafe.Pointer(&info)); err != nil {
		return info, fmt.Errorf("reading the card information: %w", err)
	}
	return info, nil
}

// readCardPlace reads where the card is. /sys/class/sound/card<N>/device
// is a link that resolves to the PCI function or the USB interface
// the card binds to, through one or more links on the way, so it is
// resolved and not joined. A USB card's identity lives one level up
// from the interface, on the device.
func readCardPlace(card int, identity *cardIdentity) {
	device, err := filepath.EvalSymlinks(fmt.Sprintf("%s/class/sound/card%d/device", sysDir, card))
	if err != nil {
		return
	}
	identity.Bus = subsystemOf(device)
	switch identity.Bus {
	case pciBus:
		identity.Location = filepath.Base(device)
	case usbBus:
		usb := usbDeviceOf(device)
		if usb == "" {
			return
		}
		identity.Location = filepath.Base(usb)
		identity.Vendor = sysfsAttribute(usb, "idVendor")
		identity.Product = sysfsAttribute(usb, "idProduct")
		identity.Serial = sysfsAttribute(usb, "serial")
	}
}

// subsystemOf names the bus a sysfs device is on. Every device has a
// subsystem link, and its last element is the bus name.
func subsystemOf(device string) string {
	target, err := os.Readlink(filepath.Join(device, "subsystem"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// usbDeviceOf climbs from a USB interface to the device that holds
// it. A sound card binds to an interface, 1-6:1.0, and the identity
// attributes live one level up on the device, 1-6. idVendor is what
// marks a device apart from an interface, because only a device
// carries one.
func usbDeviceOf(device string) string {
	for dir := device; strings.HasPrefix(dir, sysDir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir
		}
	}
	return ""
}

// sysfsAttribute reads one attribute file, and gives an empty string
// for one the device does not publish.
func sysfsAttribute(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// pcmInfoSize is the size of struct snd_pcm_info in bytes, and
// ctlIoctlPCMInfo is the request that fills one through the control
// device: the caller states the device, the subdevice, and the
// stream, and the kernel answers with the driver's own id and name
// for that PCM. It is the same answer /proc/asound/card<N>/pcm<D>p/info
// prints, from a device node this pod is given where the proc file is
// not: the container runtime masks /proc/asound with an empty tmpfs.
const pcmInfoSize = 288

var ctlIoctlPCMInfo = iowr(0x31, pcmInfoSize)

// The two stream directions struct snd_pcm_info names.
const (
	pcmStreamPlayback = 0
	pcmStreamCapture  = 1
)

// sndPCMInfo mirrors struct snd_pcm_info field for field. The fields
// after the name are here because the layout is the protocol.
type sndPCMInfo struct {
	Device          uint32
	Subdevice       uint32
	Stream          int32
	Card            int32
	ID              [64]byte
	Name            [80]byte
	Subname         [32]byte
	DevClass        int32
	DevSubclass     int32
	SubdevicesCount uint32
	SubdevicesAvail uint32
	Pad1            [16]byte
	Reserved        [64]byte
}

// readPCMInfo asks the card's control device about one PCM device.
func readPCMInfo(card, pcm int, capture bool) (sndPCMInfo, error) {
	info := sndPCMInfo{Device: uint32(pcm), Stream: pcmStreamPlayback}
	if capture {
		info.Stream = pcmStreamCapture
	}
	control, err := os.Open(fmt.Sprintf("%s/controlC%d", sndDir, card))
	if err != nil {
		return info, err
	}
	defer control.Close()
	if err := ioctl(control, ctlIoctlPCMInfo, unsafe.Pointer(&info)); err != nil {
		return info, fmt.Errorf("reading the PCM information: %w", err)
	}
	return info, nil
}

// pcmIDKey is the field of a PCM's info file that holds the driver's
// name for it.
const pcmIDKey = "id:"

// readPCMID reads the driver's own name for one PCM device, such as
// HDMI 0 or USB Audio. The id is the durable half of a device name
// and the number is not: the HDA codec fills its PCM slots from a
// fixed table per type (get_empty_pcm_device in
// sound/hda/common/codec.c), so the numbers are stable on one card,
// but a second card or a kernel change can move them, and the id
// names the same converter either way.
//
// The control device answers first, because the pod is given the
// device nodes and not /proc/asound. The proc file is the fallback
// for a control device that refuses the request, and it is what the
// fixtures in testdata answer through.
func readPCMID(card, pcm int, capture bool) string {
	if info, err := readPCMInfo(card, pcm, capture); err == nil {
		if id := cText(info.ID[:]); id != "" {
			return id
		}
	}
	direction := "p"
	if capture {
		direction = "c"
	}
	path := fmt.Sprintf("%s/asound/card%d/pcm%d%s/info", procDir, card, pcm, direction)
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if value, found := strings.CutPrefix(line, pcmIDKey); found {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// cText reads a string out of a fixed-length field the kernel fills.
// A value that fills the field has no terminator.
func cText(field []byte) string {
	if end := bytes.IndexByte(field, 0); end >= 0 {
		return string(field[:end])
	}
	return string(field)
}
