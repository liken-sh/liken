package cec

// A CEC message is at most 16 bytes. The first byte is the header, with
// the sender's logical address in the high four bits and the
// receiver's in the low four. The second byte is the opcode, and the
// rest are its operands.
//
// The opcodes and operand layouts come from the CEC supplement to the
// HDMI 1.3a specification, which hdmi.org provides free after a form
// at https://www.hdmi.org/spec/index. The kernel's linux/cec.h and
// linux/cec-funcs.h define the same values and are the open reference.

import (
	"fmt"
	"strings"
)

// Opcode is the second byte of a message.
type Opcode byte

const (
	// A device replies with Feature Abort to a directed message it does
	// not support. A test tool sends Abort to check that a device
	// replies at all. The kernel replies to Abort for its own adapter
	// and does not pass it on.
	OpFeatureAbort Opcode = 0x00
	OpAbort        Opcode = 0xff

	// One-touch play. Image View On asks the TV to leave standby and
	// show a source. Active Source is a broadcast that tells the TV and
	// any switches between which physical address is selected. Request
	// Active Source asks the current source to broadcast Active Source
	// again.
	OpImageViewOn         Opcode = 0x04
	OpTextViewOn          Opcode = 0x0d
	OpActiveSource        Opcode = 0x82
	OpInactiveSource      Opcode = 0x9d
	OpRequestActiveSource Opcode = 0x85
	OpRoutingChange       Opcode = 0x80
	OpRoutingInformation  Opcode = 0x81
	OpSetStreamPath       Opcode = 0x86
	OpStandby             Opcode = 0x36

	// Device identity. Each Give or Get message asks one device for one
	// fact, and the device replies with the matching report. Report
	// Physical Address and Device Vendor ID are broadcasts.
	OpGivePhysicalAddress Opcode = 0x83
	OpReportPhysicalAddr  Opcode = 0x84
	OpGiveOSDName         Opcode = 0x46
	OpSetOSDName          Opcode = 0x47
	OpGiveDeviceVendorID  Opcode = 0x8c
	OpDeviceVendorID      Opcode = 0x87
	OpGetCECVersion       Opcode = 0x9f
	OpCECVersion          Opcode = 0x9e

	// Power status. The kernel does not reply to Give Device Power
	// Status for its own adapter, so the program that owns the adapter
	// must reply.
	OpGiveDevicePowerStatus Opcode = 0x8f
	OpReportPowerStatus     Opcode = 0x90

	// Remote control. The TV sends its remote's button presses to the
	// active source, and the kernel turns them into key events on the
	// adapter's input device.
	OpUserControlPressed  Opcode = 0x44
	OpUserControlReleased Opcode = 0x45

	// System audio. A device sends System Audio Mode Request to ask the
	// audio system to turn the mode on, and the audio system turns it
	// on or off with Set System Audio Mode. While the mode is on, the TV
	// mutes its own speakers and sends its remote's volume keys to the
	// audio system. audio.go builds these messages.
	OpSystemAudioModeRequest Opcode = 0x70

	// Other opcodes that a follower receives. The replies and
	// notifications among them are in answers in follower.go, because a
	// follower must not send Feature Abort for a reply. The rest are
	// requests that a follower does not support.
	OpDeckStatus               Opcode = 0x1b
	OpTunerDeviceStatus        Opcode = 0x07
	OpMenuStatus               Opcode = 0x8e
	OpSetSystemAudioMode       Opcode = 0x72
	OpSystemAudioModeStatus    Opcode = 0x7e
	OpReportAudioStatus        Opcode = 0x7a
	OpReportShortAudioDesc     Opcode = 0xa3
	OpReportFeatures           Opcode = 0xa6
	OpReportCurrentLatency     Opcode = 0xa8
	OpSetMenuLanguage          Opcode = 0x32
	OpReportArcInitiated       Opcode = 0xc1
	OpReportArcTerminated      Opcode = 0xc2
	OpVendorCommand            Opcode = 0x89
	OpVendorCommandWithID      Opcode = 0xa0
	OpVendorRemoteButtonDown   Opcode = 0x8a
	OpVendorRemoteButtonUp     Opcode = 0x8b
	OpGiveDeckStatus           Opcode = 0x1a
	OpMenuRequest              Opcode = 0x8d
	OpGetMenuLanguage          Opcode = 0x91
	OpGiveAudioStatus          Opcode = 0x71
	OpGiveSystemAudioModeState Opcode = 0x7d
)

// AbortUnrecognizedOpcode is the Feature Abort reason this package
// sends.
const AbortUnrecognizedOpcode byte = 0

// A Message is one CEC frame. Body holds the opcode and the operands.
// An empty Body is a poll.
type Message struct {
	From LogicalAddress
	To   LogicalAddress
	Body []byte
}

// NewMessage builds a message from its opcode and operands.
func NewMessage(from, to LogicalAddress, opcode Opcode, operands ...byte) Message {
	return Message{From: from, To: to, Body: append([]byte{byte(opcode)}, operands...)}
}

// Poll builds a message with no opcode, which finds out whether a
// device is present at an address. A present device acknowledges the
// frame at the bus level and sends no reply message.
func Poll(from, to LogicalAddress) Message {
	return Message{From: from, To: to}
}

func (m Message) IsPoll() bool {
	return len(m.Body) == 0
}

func (m Message) IsBroadcast() bool {
	return m.To == AddressBroadcast
}

// Opcode returns false for a poll.
func (m Message) Opcode() (Opcode, bool) {
	if m.IsPoll() {
		return 0, false
	}
	return Opcode(m.Body[0]), true
}

func (m Message) Operands() []byte {
	if len(m.Body) < 2 {
		return nil
	}
	return m.Body[1:]
}

// String formats the message for a log line, in hex.
func (m Message) String() string {
	var text strings.Builder
	fmt.Fprintf(&text, "%x->%x", uint8(m.From), uint8(m.To))
	for _, value := range m.Body {
		fmt.Fprintf(&text, " %02x", value)
	}
	return text.String()
}

func GivePhysicalAddress(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpGivePhysicalAddress)
}

func GiveOSDName(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpGiveOSDName)
}

func GiveDeviceVendorID(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpGiveDeviceVendorID)
}

func GetCECVersion(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpGetCECVersion)
}

func GiveDevicePowerStatus(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpGiveDevicePowerStatus)
}

func ReportPowerStatus(from, to LogicalAddress, power PowerStatus) Message {
	return NewMessage(from, to, OpReportPowerStatus, byte(power))
}

func FeatureAbort(from, to LogicalAddress, opcode Opcode, reason byte) Message {
	return NewMessage(from, to, OpFeatureAbort, byte(opcode), reason)
}

func ReportPhysicalAddress(from LogicalAddress, address PhysicalAddress, primaryType byte) Message {
	return NewMessage(from, AddressBroadcast, OpReportPhysicalAddr, byte(address>>8), byte(address), primaryType)
}

func SetOSDName(from, to LogicalAddress, name string) Message {
	return NewMessage(from, to, OpSetOSDName, []byte(name)...)
}

func DeviceVendorID(from LogicalAddress, vendor VendorID) Message {
	return NewMessage(from, AddressBroadcast, OpDeviceVendorID, byte(vendor>>16), byte(vendor>>8), byte(vendor))
}

func CECVersionReport(from, to LogicalAddress, version Version) Message {
	return NewMessage(from, to, OpCECVersion, byte(version))
}

func ActiveSource(from LogicalAddress, address PhysicalAddress) Message {
	return NewMessage(from, AddressBroadcast, OpActiveSource, byte(address>>8), byte(address))
}

// InactiveSource tells the TV that the source at an address has no
// picture to show. It is sent to the TV only (HDMI-CEC 1.3a, CEC
// Table 8).
func InactiveSource(from LogicalAddress, address PhysicalAddress) Message {
	return NewMessage(from, AddressTV, OpInactiveSource, byte(address>>8), byte(address))
}

func ImageViewOn(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpImageViewOn)
}

// Standby puts one device in standby, or every device on the bus when
// to is the broadcast address.
func Standby(from, to LogicalAddress) Message {
	return NewMessage(from, to, OpStandby)
}

// PowerStatus is the operand of Report Power Status.
type PowerStatus byte

const (
	PowerOn        PowerStatus = 0
	PowerStandby   PowerStatus = 1
	PowerToOn      PowerStatus = 2
	PowerToStandby PowerStatus = 3
	// PowerUnknown is not a CEC operand value. It marks a device that
	// has not reported its power.
	PowerUnknown PowerStatus = 0xff
)

// String returns the name that the CECBus and Television status write
// for the power state, or an empty string for any other value.
func (p PowerStatus) String() string {
	switch p {
	case PowerOn:
		return "On"
	case PowerStandby:
		return "Standby"
	case PowerToOn:
		return "ToOn"
	case PowerToStandby:
		return "ToStandby"
	}
	return ""
}

// Version is the operand of CEC Version.
type Version byte

const (
	Version13a     Version = 4
	Version14      Version = 5
	Version20      Version = 6
	VersionUnknown Version = 0
)

// String returns the version number, or an empty string for a value
// this package does not define.
func (v Version) String() string {
	switch v {
	case Version13a:
		return "1.3a"
	case Version14:
		return "1.4"
	case Version20:
		return "2.0"
	}
	return ""
}

// VendorID is a device's IEEE OUI, 24 bits.
type VendorID uint32

// VendorUnknown marks a device that has not reported a vendor. It is
// the kernel's value for an adapter with no vendor ID.
const VendorUnknown VendorID = vendorIDNone

// vendorZero is the vendor ID 000000, which a device can report but
// which belongs to no vendor (see directory.go).
const vendorZero VendorID = 0

// String formats the OUI as six lowercase hex digits, or an empty
// string for VendorUnknown.
func (v VendorID) String() string {
	if v == VendorUnknown {
		return ""
	}
	return fmt.Sprintf("%06x", uint32(v))
}
