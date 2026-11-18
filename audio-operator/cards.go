package main

// liken runs no udev, so the operator reads the card attributes that
// udev's 60-persistent-alsa.rules reads to build /dev/snd/by-id and
// /dev/snd/by-path, and names.go builds the same names from them. The
// card's control device, /dev/snd/controlC<N>, returns the card's id,
// driver, and name, and the driver's id for each PCM device, through
// ioctls. sysfs gives the card's PCI function or USB port, and for a
// USB card the vendor, product, and serial of the USB device.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// Tests point these at fixture trees.
var (
	sysDir  = "/sys"
	procDir = "/proc"
)

// ctlCardInfoSize is sizeof(struct snd_ctl_card_info). The ioctl
// request number encodes it, so a wrong size makes the kernel reject
// the request.
const ctlCardInfoSize = 376

// ctlIoctlCardInfo is SNDRV_CTL_IOCTL_CARD_INFO. It is _IOR, not
// _IOWR, because the kernel fills the whole structure and reads
// nothing from it.
var ctlIoctlCardInfo = ior(0x01, ctlCardInfoSize)

// ior builds a request number the way the kernel's _IOR macro does.
// 'U' is the ioctl type of ALSA's control devices.
func ior(number, size uintptr) uintptr {
	return 2<<30 | size<<16 | 'U'<<8 | number
}

// ctlCardInfo mirrors struct snd_ctl_card_info. The fields the
// operator does not read stay, because the kernel writes the whole
// structure and dropping a field would shift every field after it.
type ctlCardInfo struct {
	Card       int32
	Pad        int32
	ID         [16]byte
	Driver     [16]byte
	Name       [32]byte
	LongName   [80]byte
	Reserved   [16]byte
	MixerName  [80]byte
	Components [128]byte
}

// ID, Driver, and Name come from the control device, and the other
// fields come from sysfs. names.go builds the device name from the
// sysfs fields.
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

// names.go refuses a card on any other bus.
const (
	pciBus = "pci"
	usbBus = "usb"
)

// readCardIdentity leaves a field empty when it cannot read it, and
// returns no error, because names.go refuses an endpoint that lacks a
// field its name needs, and that error names the field.
func readCardIdentity(card int) cardIdentity {
	identity := cardIdentity{}
	if info, err := readCardInfo(card); err == nil {
		identity.ID = cText(info.ID[:])
		identity.Driver = cText(info.Driver[:])
		identity.Name = cText(info.Name[:])
	}
	readCardLocation(card, &identity)
	return identity
}

func readCardInfo(card int) (ctlCardInfo, error) {
	var info ctlCardInfo
	control, err := os.Open(fmt.Sprintf("%s/controlC%d", sndDir, card))
	if err != nil {
		return info, err
	}
	defer control.Close()
	if err := ioctl(control, ctlIoctlCardInfo, unsafe.Pointer(&info)); err != nil {
		return info, fmt.Errorf("reading the card information from controlC%d: %w", card, err)
	}
	return info, nil
}

// readCardLocation fills in the bus, the location, and the USB
// attributes. /sys/class/sound/card<N>/device is a chain of symbolic
// links that ends at the PCI function or the USB interface the card is
// bound to, so the path is resolved with EvalSymlinks and not joined by
// hand.
func readCardLocation(card int, identity *cardIdentity) {
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

func subsystemOf(device string) string {
	target, err := os.Readlink(filepath.Join(device, "subsystem"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// usbDeviceOf returns the USB device that contains a USB interface. A
// sound card binds to an interface such as 1-6:1.0, but vendor,
// product, and serial are attributes of the device above it, 1-6. Only
// a device directory has an idVendor file.
func usbDeviceOf(device string) string {
	for dir := device; strings.HasPrefix(dir, sysDir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir
		}
	}
	return ""
}

func sysfsAttribute(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// pcmInfoSize is sizeof(struct snd_pcm_info). For
// SNDRV_CTL_IOCTL_PCM_INFO, the caller sets the device, subdevice, and
// stream, and the kernel fills in the driver's id and name for that
// PCM, the same text as /proc/asound/card<N>/pcm<D>p/info. The pod
// cannot read that file, because the container runtime masks
// /proc/asound with an empty tmpfs, but it has the control device node.
const pcmInfoSize = 288

var ctlIoctlPCMInfo = iowr(0x31, pcmInfoSize)

const (
	pcmStreamPlayback = 0
	pcmStreamCapture  = 1
)

// sndPCMInfo mirrors struct snd_pcm_info. The fields after Name are
// never read, and stay because the kernel writes the whole structure.
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
		return info, fmt.Errorf("reading the information of PCM device %d from controlC%d: %w", pcm, card, err)
	}
	return info, nil
}

const pcmIDKey = "id:"

// readPCMID returns the driver's id for a PCM device, such as "HDMI 0"
// or "USB Audio", or "" when it cannot be read. Device names use this
// id and not the PCM number, because the id names the same converter
// when the number changes. The HDA driver assigns PCM numbers from a
// fixed table for each type of PCM (get_empty_pcm_device in
// sound/hda/common/codec.c), so the numbers are stable on one card, but
// a different card or a kernel update can move them.
//
// The ioctl comes first, because the pod has the device nodes and not
// /proc/asound. The /proc file is read when the ioctl fails or returns
// an empty id, and the test fixtures supply the id only through that
// file.
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

// cText converts a NUL-terminated field that the kernel fills into a
// string. A value that fills the whole field has no NUL.
func cText(field []byte) string {
	if end := bytes.IndexByte(field, 0); end >= 0 {
		return string(field[:end])
	}
	return string(field)
}
