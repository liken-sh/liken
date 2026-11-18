package main

// This file reads the CEC physical address of the HDMI input that the
// machine is plugged into, and publishes it as status.physicalAddress
// of the Display.
//
// A physical address is four hex digits, A.B.C.D, with one digit for
// each HDMI input on the path from the TV. The TV is 0.0.0.0, a
// receiver on the TV's input 1 is 1.0.0.0, and a machine on that
// receiver's input 3 is 1.3.0.0. The HDMI specification requires a
// sink to put the address of each input in the HDMI vendor-specific
// data block of the EDID that it serves on that input, so the EDID on
// this machine's connector has the machine's address.
//
// A USB-CEC adapter reads no EDID, so it has no valid address of its
// own. equipment-operator reads status.physicalAddress and sets it on
// the adapter, so that the adapter's Active Source message switches the
// receiver and the TV to this machine. display-operator never opens a
// CEC adapter.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// PhysicalAddressCurrentCondition is True when status.physicalAddress
// comes from the EDID that the connector serves now.
const PhysicalAddressCurrentCondition = "PhysicalAddressCurrent"

// In a CTA-861 extension, the data blocks start at byte 4 and end at
// the offset in byte 2. Each block's header byte has the tag in its top
// three bits and the payload length in its low five bits.
const (
	ctaBlocksStart = 4
	ctaVendorTag   = 3
)

// The HDMI Licensing OUI, 00-0C-03, in the byte order that the block
// stores it, least significant byte first.
var hdmiVendorOUI = [3]byte{0x03, 0x0c, 0x00}

// physicalAddress returns the address in the HDMI vendor block of one
// CTA-861 extension, or the empty string when the extension has no
// valid address. The caller has already checked the extension's tag,
// revision, and checksum.
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

// addressText formats the two address bytes as A.B.C.D, the way CEC
// tools print them. It returns the empty string for an address that
// cannot be this machine's:
//
//   - 0.0.0.0 is the TV's own address. A sink serves it when it has no
//     address to give, and a machine that published it would claim to
//     be the TV.
//   - f.f.f.f is the address that the CEC specification reserves for a
//     device that has none.
//   - A non-zero digit after a zero digit, such as 1.0.2.0. A zero ends
//     the path, so the address is malformed.
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

// ambiguousAddresses returns a message for each monitor, keyed by its
// monitorID, whose connected outputs serve different physical
// addresses.
//
// Pulse-Eight's two-cable setup produces this. One cable carries the
// picture into one input of a receiver, and a second cable goes
// through a CEC adapter into another input of the same receiver. The
// receiver serves its own EDID on both connectors, so they have one
// monitorID, but each EDID has the address of the input that its cable
// is plugged into. Neither address is the right one to give to a CEC
// adapter, so the operator publishes no new address while the two
// disagree.
func ambiguousAddresses(outputs []Output) map[string]string {
	byIdentity := map[string][]Output{}
	for _, output := range outputs {
		if !output.Connected {
			continue
		}
		if name := monitorID(output.Monitor); name != "" {
			byIdentity[name] = append(byIdentity[name], output)
		}
	}
	messages := map[string]string{}
	for name, group := range byIdentity {
		if message, ambiguous := ambiguousAddress(group); ambiguous {
			messages[name] = message
		}
	}
	return messages
}

// ambiguousAddress reports whether the connectors of one monitor serve
// two or more different valid addresses. A connector with no valid
// address does not count. The message lists the connectors sorted by
// name, so the order of outputs does not change the message and cause
// a status write.
func ambiguousAddress(outputs []Output) (string, bool) {
	byAddress := map[string]Output{}
	for _, output := range outputs {
		if output.Monitor.PhysicalAddress != "" {
			byAddress[output.Monitor.PhysicalAddress] = output
		}
	}
	if len(byAddress) < 2 {
		return "", false
	}
	named := make([]Output, 0, len(byAddress))
	for _, output := range byAddress {
		named = append(named, output)
	}
	slices.SortFunc(named, func(a, b Output) int {
		return strings.Compare(a.Connector, b.Connector)
	})
	parts := make([]string, len(named))
	for i, output := range named {
		parts[i] = fmt.Sprintf("%s reports %s", output.Connector, output.Monitor.PhysicalAddress)
	}
	return strings.Join(parts, " and ") +
		" for this monitor. status.physicalAddress is not updated while the connectors report different addresses", true
}

// withPhysicalAddress sets status.physicalAddress and its condition
// from the connector's EDID. ambiguous is the message from
// ambiguousAddresses for this monitor, or empty.
//
// status.physicalAddress keeps its last valid value when the EDID has
// no valid address and while the connectors disagree. A receiver in
// standby can serve an EDID with no vendor block, and a CEC adapter
// that already uses the last valid address keeps working.
func (d *displayControl) withPhysicalAddress(status DisplayStatus, output Output, ambiguous string) DisplayStatus {
	if ambiguous != "" {
		status.Conditions = setCondition(status.Conditions, d.condition(PhysicalAddressCurrentCondition, false,
			AmbiguousReason, ambiguous))
		return status
	}
	address := output.Monitor.PhysicalAddress
	if address == "" {
		return d.retainAddress(status,
			"The EDID on "+output.Connector+" has no valid physical address")
	}
	status.PhysicalAddress = address
	status.Conditions = setCondition(status.Conditions, d.condition(PhysicalAddressCurrentCondition, true,
		ReadFromEDIDReason, fmt.Sprintf("The EDID on %s has the physical address %s", output.Connector, address)))
	return status
}

// retainAddress keeps the last valid address of a Display whose
// connector serves no valid address now, and sets the condition to
// False with cause in its message. For a Display that never had a valid
// address, it changes nothing.
//
// The message gives the condition's last transition time, which
// setCondition keeps while the condition stays False. The current time
// would change the message, and so cause a status write, on every pass.
func (d *displayControl) retainAddress(status DisplayStatus, cause string) DisplayStatus {
	if status.PhysicalAddress == "" {
		return status
	}
	since := d.now().UTC().Format(time.RFC3339)
	if current := currentCondition(status, PhysicalAddressCurrentCondition); current.Status == conditionFalse {
		since = current.LastTransitionTime.Format(time.RFC3339)
	}
	status.Conditions = setCondition(status.Conditions, d.condition(PhysicalAddressCurrentCondition, false,
		RetainedReason, fmt.Sprintf("%s. status.physicalAddress keeps the address that the EDID had until %s", cause, since)))
	return status
}

// currentCondition returns the zero condition when the status has
// none of the given type.
func currentCondition(status DisplayStatus, kind string) DisplayCondition {
	for _, condition := range status.Conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return DisplayCondition{}
}
