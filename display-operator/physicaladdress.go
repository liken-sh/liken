package main

// The CEC physical address of the machine's port.
//
// CEC is the control channel that every HDMI port carries. Every
// device on a CEC bus has a physical address: four hex digits, A.B.C.D,
// that give the path from the TV to the device, one digit for each HDMI
// port on the way. The TV is 0.0.0.0, a receiver on the TV's input 1 is
// 1.0.0.0, and a machine on that receiver's input 3 is 1.3.0.0.
//
// A device learns its address from the EDID its sink serves. The HDMI
// specification requires a sink to serve each of its HDMI ports an EDID
// whose HDMI vendor-specific data block carries that port's address,
// and a receiver writes the address of each of its inputs into the EDID
// it serves on that input. So the EDID on this machine's connector
// states the address of the port where the machine's picture enters.
//
// A USB-CEC adapter has no EDID to read, and it starts with no valid
// address. The address is published for a reader that speaks CEC, such
// as equipment-operator, which gives the adapter this address so that
// the adapter's Active Source message switches the receiver and the TV
// to this machine's picture. This operator reads and publishes the
// address; it never opens a CEC adapter or sends a CEC message.

import (
	"fmt"
	"time"
)

// The condition that states where status.physicalAddress came from,
// and its two reasons. ReadFromEDID is an address the connector's
// current EDID serves. Retained is the last valid address, kept while
// the connector serves no EDID for this monitor or serves no valid
// address in it.
const (
	PhysicalAddressCurrentCondition = "PhysicalAddressCurrent"
	ReadFromEDIDReason              = "ReadFromEDID"
	RetainedReason                  = "Retained"
)

// The CTA-861 data block collection starts at byte 4 of the extension
// and ends where the descriptors start, at the offset byte 2 holds.
// Each block starts with a byte that holds the block's tag in its top
// three bits and the length of its payload in its low five, so the
// walk steps over the blocks it does not read with no other table.
const (
	ctaBlocksStart = 4
	ctaVendorTag   = 3
)

// The HDMI Licensing OUI, 00-0C-03, as the block stores it: least
// significant byte first.
var hdmiVendorOUI = [3]byte{0x03, 0x0c, 0x00}

// physicalAddress walks one CTA-861 extension's data blocks for the
// HDMI vendor block, and answers the address behind the OUI in its
// dotted form. It answers the empty string for an extension with no
// HDMI vendor block, for a block too short to carry an address, and for
// an address that names no path.
//
// The caller checked the tag, the revision, and the checksum.
func physicalAddress(extension []byte) string {
	end := int(extension[2])
	if end > blockSize {
		return ""
	}
	for offset := ctaBlocksStart; offset < end; {
		header := extension[offset]
		tag, length := int(header>>5), int(header&0x1f)
		payload := offset + 1
		if payload+length > end {
			return ""
		}
		if tag == ctaVendorTag && length >= 5 &&
			[3]byte(extension[payload:payload+3]) == hdmiVendorOUI {
			return addressText(extension[payload+3], extension[payload+4])
		}
		offset = payload + length
	}
	return ""
}

// addressText reads the two address bytes as four nibbles, A.B.C.D,
// high nibble first, and answers them in the dotted form that CEC
// tools print.
//
// Three forms name no path, and each answers the empty string:
//
//   - 0.0.0.0 is the TV's own address. A sink serves it when it states
//     no address, and a machine that announced it would claim to be
//     the TV.
//   - f.f.f.f is the invalid address, which the CEC specification
//     reserves for a device that has none.
//   - A non-zero digit after a zero digit, such as 1.0.2.0. A zero ends
//     the path, so the digits after it name no port.
func addressText(high, low byte) string {
	digits := [4]byte{high >> 4, high & 0x0f, low >> 4, low & 0x0f}
	if digits == [4]byte{} || digits == [4]byte{0xf, 0xf, 0xf, 0xf} {
		return ""
	}
	for i := 1; i < len(digits); i++ {
		if digits[i-1] == 0 && digits[i] != 0 {
			return ""
		}
	}
	return fmt.Sprintf("%x.%x.%x.%x", digits[0], digits[1], digits[2], digits[3])
}

// withPhysicalAddress sets status.physicalAddress and its condition for
// a monitor whose connector serves an EDID for it now.
//
// The field keeps the last valid address when the current EDID states
// none. A receiver in standby can serve an EDID with no vendor block,
// and the machine's port has not moved while it sleeps.
func (d *displayControl) withPhysicalAddress(status DisplayStatus, output Output) DisplayStatus {
	address := output.Monitor.PhysicalAddress
	if address == "" {
		return d.retainAddress(status,
			output.Connector+" serves no valid physical address in its EDID")
	}
	status.PhysicalAddress = address
	status.Conditions = setCondition(status.Conditions, d.condition(PhysicalAddressCurrentCondition, true,
		ReadFromEDIDReason, fmt.Sprintf("%s serves %s in its EDID", output.Connector, address)))
	return status
}

// retainAddress reports a Display whose connector serves no valid
// address for it now. A Display that has never served a valid address
// carries no condition, because it has no address to retain.
//
// The message states the time the connector stopped serving the
// address: the moment the condition turned False, which setCondition
// keeps across passes while the status stays False. A time read from
// the clock on every pass would change the message and make every pass
// a status write.
func (d *displayControl) retainAddress(status DisplayStatus, cause string) DisplayStatus {
	if status.PhysicalAddress == "" {
		return status
	}
	since := d.now().UTC().Format(time.RFC3339)
	if current := currentCondition(status, PhysicalAddressCurrentCondition); current.Status == conditionFalse {
		since = current.LastTransitionTime
	}
	status.Conditions = setCondition(status.Conditions, d.condition(PhysicalAddressCurrentCondition, false,
		RetainedReason, fmt.Sprintf("%s; this is the address it served until %s", cause, since)))
	return status
}

// currentCondition answers the condition of one type, or the zero
// condition when the status carries none.
func currentCondition(status DisplayStatus, kind string) DisplayCondition {
	for _, condition := range status.Conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return DisplayCondition{}
}
