package cec

// The two addresses every CEC device has. The kernel's CEC
// introduction explains both:
// https://docs.kernel.org/userspace-api/media/cec/cec-intro.html

import (
	"fmt"
	"strconv"
	"strings"
)

// A PhysicalAddress is a device's place in the HDMI tree, four numbers
// from 0 to 15 packed into sixteen bits. The TV is 0.0.0.0. A receiver
// on the TV's input 1 is 1.0.0.0, and a machine on that receiver's
// input 3 is 1.3.0.0. A source reads its address from the EDID of the
// device its cable goes to, so the address changes when the cables
// change.
type PhysicalAddress uint16

// InvalidPhysicalAddress is f.f.f.f, the address of a device that has
// not read one. The kernel reports it for an adapter with no address.
const InvalidPhysicalAddress PhysicalAddress = 0xffff

// String writes the address in the dotted form the CEC tools print.
func (p PhysicalAddress) String() string {
	value := uint16(p)
	return fmt.Sprintf("%x.%x.%x.%x", value>>12, value>>8&0xf, value>>4&0xf, value&0xf)
}

// ParsePhysicalAddress reads the dotted form, such as 1.3.0.0. It
// refuses f.f.f.f, because a device can never announce that address.
func ParsePhysicalAddress(text string) (PhysicalAddress, error) {
	parts := strings.Split(text, ".")
	if len(parts) != 4 {
		return InvalidPhysicalAddress, fmt.Errorf("physical address %q does not have four parts", text)
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

// A LogicalAddress is the number from 0 to 15 that a device claims
// when it joins the bus. The number states the device's type. 0 is
// the TV and 5 is the audio system, and each of those two exists at
// most once on a bus. 15 is "unregistered" when it sends a message and
// "broadcast" when it receives one.
type LogicalAddress uint8

const (
	AddressTV           LogicalAddress = 0
	AddressAudioSystem  LogicalAddress = 5
	AddressUnregistered LogicalAddress = 15
	AddressBroadcast    LogicalAddress = 15
)

// DeviceType is the kind of device, as CEC names it. The primary
// device type in Report Physical Address states it, and each logical
// address also implies one.
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

// logicalTypes is the type each logical address implies. Playback
// devices take 4, 8, and 11; recording devices take 1, 2, and 9;
// tuners take 3, 6, 7, and 10. 12 and 13 are backup addresses and 14
// is free use, from CEC 2.0.
var logicalTypes = [16]DeviceType{
	TypeTV, TypeRecording, TypeRecording, TypeTuner,
	TypePlayback, TypeAudioSystem, TypeTuner, TypeTuner,
	TypePlayback, TypeRecording, TypeTuner, TypePlayback,
	TypeBackup, TypeBackup, TypeSpecific, TypeUnregistered,
}

// Type is the device type the address implies.
func (l LogicalAddress) Type() DeviceType {
	return logicalTypes[l&0xf]
}

// primaryTypes maps the primary device type operand of Report Physical
// Address. A switch and a video processor have no logical address of
// their own, so only this operand names them.
var primaryTypes = map[byte]DeviceType{
	0: TypeTV,
	1: TypeRecording,
	3: TypeTuner,
	4: TypePlayback,
	5: TypeAudioSystem,
	6: TypeSwitch,
	7: TypeProcessor,
}
