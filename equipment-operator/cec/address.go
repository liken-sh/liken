package cec

// A CEC device has a physical address and a logical address. The
// kernel's CEC introduction describes both:
// https://docs.kernel.org/userspace-api/media/cec/cec-intro.html

import (
	"fmt"
	"strconv"
	"strings"
)

// A PhysicalAddress is a device's position in the HDMI tree: four
// numbers from 0 to 15, packed into 16 bits. The TV is 0.0.0.0. A
// receiver on the TV's input 1 is 1.0.0.0, and a machine on that
// receiver's input 3 is 1.3.0.0. A source reads its address from the
// EDID of the device that its cable goes to, so the address changes
// when the cabling changes.
type PhysicalAddress uint16

// InvalidPhysicalAddress is f.f.f.f, the address of a device that has
// not read one. The kernel reports it for an adapter with no address.
const InvalidPhysicalAddress PhysicalAddress = 0xffff

// String returns the address in the dotted form that the CEC tools
// print.
func (p PhysicalAddress) String() string {
	value := uint16(p)
	return fmt.Sprintf("%x.%x.%x.%x", value>>12, value>>8&0xf, value>>4&0xf, value&0xf)
}

// ParsePhysicalAddress parses the dotted form, such as 1.3.0.0. It
// returns an error for f.f.f.f, because no device announces that
// address.
func ParsePhysicalAddress(text string) (PhysicalAddress, error) {
	parts := strings.Split(text, ".")
	if len(parts) != 4 {
		return InvalidPhysicalAddress, fmt.Errorf("physical address %q has %d parts, want 4", text, len(parts))
	}
	var address PhysicalAddress
	for _, part := range parts {
		digit, err := strconv.ParseUint(part, 16, 4)
		if err != nil {
			return InvalidPhysicalAddress, fmt.Errorf("physical address %q: part %q is not a digit from 0 to f", text, part)
		}
		address = address<<4 | PhysicalAddress(digit)
	}
	if address == InvalidPhysicalAddress {
		return InvalidPhysicalAddress, fmt.Errorf("physical address %q is the invalid address", text)
	}
	return address, nil
}

// depth is the number of nonzero parts before the first 0: 0 for the
// TV, 1 for 1.0.0.0, and 2 for 1.3.0.0.
func (p PhysicalAddress) depth() int {
	for level := range 4 {
		if p>>(12-4*level)&0xf == 0 {
			return level
		}
	}
	return 4
}

// Above reports whether the other address is in the subtree below this
// one, which means that the other device connects to the TV through
// the device at this address. A receiver at 1.0.0.0 is above a machine
// on its input 3 at 1.3.0.0, and is not above a machine on the TV's
// input 2 at 2.0.0.0. An address is not above itself.
func (p PhysicalAddress) Above(other PhysicalAddress) bool {
	depth := p.depth()
	if other.depth() <= depth {
		return false
	}
	shift := 16 - 4*depth
	return p>>shift == other>>shift
}

// A LogicalAddress is the number from 0 to 15 that a device claims
// when it joins the bus. The number depends on the device's type. 0 is
// the TV and 5 is the audio system, and a bus has at most one of each.
// 15 means "unregistered" as the sender of a message and "broadcast" as
// its destination.
type LogicalAddress uint8

const (
	AddressTV           LogicalAddress = 0
	AddressAudioSystem  LogicalAddress = 5
	AddressUnregistered LogicalAddress = 15
	AddressBroadcast    LogicalAddress = 15
)

// DeviceType is a kind of device, with the names that the CEC
// specification uses. A device reports its primary type in Report
// Physical Address, and each logical address also implies a type.
type DeviceType string

const (
	TypeTV           DeviceType = "TV"
	TypeRecording    DeviceType = "Recording"
	TypeTuner        DeviceType = "Tuner"
	TypePlayback     DeviceType = "Playback"
	TypeAudioSystem  DeviceType = "AudioSystem"
	TypeSwitch       DeviceType = "Switch"
	TypeProcessor    DeviceType = "Processor"
	TypeBackup       DeviceType = "Backup"
	TypeSpecific     DeviceType = "Specific"
	TypeUnregistered DeviceType = "Unregistered"
)

// logicalTypes is the type that each logical address implies. 12 and
// 13 are for backup devices and 14 is for free use, from CEC 2.0.
var logicalTypes = [16]DeviceType{
	TypeTV, TypeRecording, TypeRecording, TypeTuner,
	TypePlayback, TypeAudioSystem, TypeTuner, TypeTuner,
	TypePlayback, TypeRecording, TypeTuner, TypePlayback,
	TypeBackup, TypeBackup, TypeSpecific, TypeUnregistered,
}

// Type returns the device type that the address implies.
func (l LogicalAddress) Type() DeviceType {
	return logicalTypes[l&0xf]
}

// primaryTypes maps the primary device type operand of Report Physical
// Address to a DeviceType. A switch and a video processor have no
// logical address that implies their type, so only this operand
// identifies them.
var primaryTypes = map[byte]DeviceType{
	0: TypeTV,
	1: TypeRecording,
	3: TypeTuner,
	4: TypePlayback,
	5: TypeAudioSystem,
	6: TypeSwitch,
	7: TypeProcessor,
}
