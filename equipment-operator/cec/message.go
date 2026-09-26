package cec

// The CEC messages this package sends and reads. A CEC message is at
// most sixteen bytes. The first byte is the header: the sender's
// logical address in the high four bits and the receiver's in the low
// four. The second byte is the opcode, and the rest are its operands.
// A message with a header and no opcode is a poll: a device sends it
// to find out whether another device answers at an address.
//
// The opcodes and the operand layouts are those of the CEC supplement
// to the HDMI specification. HDMI 1.3a with its CEC supplement is free
// from https://www.hdmi.org/spec/index after a form. The kernel's
// linux/cec.h and linux/cec-funcs.h carry the same values and are the
// open reference this file follows.

import (
	"fmt"
	"strings"
)

// Opcode is the second byte of a message.
type Opcode byte

// The message families this package uses. The comment on each group
// says which device sends the message and what answers it.
const (
	// Feature Abort answers a directed message the receiver does not
	// support. Abort asks a device for a Feature Abort, which a test
	// tool uses to check that a device answers at all; the kernel
	// answers it for its own adapter and does not pass it on.
	OpFeatureAbort Opcode = 0x00
	OpAbort        Opcode = 0xff

	// The one-touch play family. Image View On asks the TV to leave
	// standby and show a source. Active Source is a broadcast that
	// says which physical address the TV and the switches between
	// select. Request Active Source asks the current source to
	// broadcast Active Source again.
	OpImageViewOn         Opcode = 0x04
	OpTextViewOn          Opcode = 0x0d
	OpActiveSource        Opcode = 0x82
	OpInactiveSource      Opcode = 0x9d
	OpRequestActiveSource Opcode = 0x85
	OpRoutingChange       Opcode = 0x80
	OpRoutingInformation  Opcode = 0x81
	OpSetStreamPath       Opcode = 0x86
	OpStandby             Opcode = 0x36

	// The identity family. Each Give message asks one device for one
	// fact, and the device answers with the matching report. Report
	// Physical Address and Device Vendor ID are broadcasts, so every
	// device on the bus hears the answer.
	OpGivePhysicalAddress Opcode = 0x83
	OpReportPhysicalAddr  Opcode = 0x84
	OpGiveOSDName         Opcode = 0x46
	OpSetOSDName          Opcode = 0x47
	OpGiveDeviceVendorID  Opcode = 0x8c
	OpDeviceVendorID      Opcode = 0x87
	OpGetCECVersion       Opcode = 0x9f
	OpCECVersion          Opcode = 0x9e

	// The power family. The kernel does not answer Give Device Power
	// Status for its own adapter, so a program that follows the
	// adapter answers it.
	OpGiveDevicePowerStatus Opcode = 0x8f
	OpReportPowerStatus     Opcode = 0x90

	// The remote-control family. The TV sends the buttons of its own
	// remote to the active source, and the kernel turns them into key
	// events on the adapter's input device.
	OpUserControlPressed  Opcode = 0x44
	OpUserControlReleased Opcode = 0x45

	// The answers from other families that can arrive at a follower
	// after a request. A follower must not answer an answer with a
	// Feature Abort.
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

// The Feature Abort reason this package sends.
const AbortUnrecognizedOpcode byte = 0

// A Message is one CEC frame. Body holds the opcode and the operands;
// an empty Body is a poll.
type Message struct {
	From LogicalAddress
	To   LogicalAddress
	Body []byte
}

// NewMessage builds a message from its opcode and operands.
func NewMessage(from, to LogicalAddress, opcode Opcode, operands ...byte) Message {
	return Message{From: from, To: to, Body: append([]byte{byte(opcode)}, operands...)}
}

// Poll builds the message that asks whether a device answers at an
// address. The answer is the acknowledge bit, not a reply.
func Poll(from, to LogicalAddress) Message {
	return Message{From: from, To: to}
}

// IsPoll answers whether the message has no opcode.
func (m Message) IsPoll() bool {
	return len(m.Body) == 0
}

// IsBroadcast answers whether every device on the bus receives the
// message.
func (m Message) IsBroadcast() bool {
	return m.To == AddressBroadcast
}

// Opcode is the message's opcode, and false for a poll.
func (m Message) Opcode() (Opcode, bool) {
	if m.IsPoll() {
		return 0, false
	}
	return Opcode(m.Body[0]), true
}

// Operands are the bytes after the opcode.
func (m Message) Operands() []byte {
	if len(m.Body) < 2 {
		return nil
	}
	return m.Body[1:]
}

// String writes the message the way a log line reads it: the two
// addresses and the bytes in hex.
func (m Message) String() string {
	var text strings.Builder
	fmt.Fprintf(&text, "%x->%x", uint8(m.From), uint8(m.To))
	for _, value := range m.Body {
		fmt.Fprintf(&text, " %02x", value)
	}
	return text.String()
}

// The messages this package builds, one function each, so a caller
// never writes an opcode byte by hand.

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

// PowerStatus is the operand of Report Power Status.
type PowerStatus byte

const (
	PowerOn        PowerStatus = 0
	PowerStandby   PowerStatus = 1
	PowerToOn      PowerStatus = 2
	PowerToStandby PowerStatus = 3
	// PowerUnknown is no operand value. It marks a device that has not
	// reported its power.
	PowerUnknown PowerStatus = 0xff
)

// String names the power the way the status reports it. A device in
// the two transitions reports ToOn or ToStandby.
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

// String names the version the way the HDMI specification numbers it.
// A value this package does not know reads as empty.
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
// the value the kernel uses for an adapter with none.
const VendorUnknown VendorID = vendorIDNone

// String writes the OUI as six lowercase hex digits, the way the IEEE
// registry lists it without separators.
func (v VendorID) String() string {
	if v == VendorUnknown {
		return ""
	}
	return fmt.Sprintf("%06x", uint32(v))
}
