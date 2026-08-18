package main

// The identity of a published device.
//
// An output's name is its ALSA card number and PCM device number,
// such as card0-pcm3. The number comes from the codec's pin order,
// which the driver enumerates the same way at every boot on the same
// hardware and kernel. It is not stable across machines, and this
// operator does not claim that it survives a kernel change. A claim
// that must survive either one selects on the attributes instead.
//
// The pairing identity is a different thing. monitor.liken.sh/id
// names the monitor a claim asks for, in a domain that neither this
// driver nor the display operator owns, so that one ResourceClaim can
// hold a request against each driver and a matchAttribute constraint
// across the two. The scheduler compares a fully qualified attribute
// name across devices without regard to which driver published them.
// An attribute written without a domain belongs to the publishing
// driver's domain, so a bare monitorName from two drivers is two
// different names that never match.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PairingAttribute is the fully qualified attribute that pairs a
// screen with its speakers. The display operator publishes the same
// name, and the two values must be identical byte for byte, so the
// derivation below is the contract between the two repositories.
const PairingAttribute = "monitor.liken.sh/id"

// deviceName builds the DRA device name for one output. A device name
// must be a DNS label, which admits lowercase letters, digits, and
// dashes.
func deviceName(card, pcm int) string {
	return fmt.Sprintf("card%d-pcm%d", card, pcm)
}

// deviceNamePattern is what deviceName writes, and nothing else.
var deviceNamePattern = regexp.MustCompile(`^card(\d+)-pcm(\d+)$`)

// outputFromDeviceName inverts deviceName. A DRA prepare call carries
// the allocated device's name and nothing else, so this is what tells
// the plugin which output a claim holds. A name that this driver did
// not write resolves to no output, and the prepare call fails with
// that name in its message.
func outputFromDeviceName(name string) (card, pcm int, ok bool) {
	match := deviceNamePattern.FindStringSubmatch(name)
	if match == nil {
		return 0, 0, false
	}
	card, cardErr := strconv.Atoi(match[1])
	pcm, pcmErr := strconv.Atoi(match[2])
	if cardErr != nil || pcmErr != nil {
		return 0, 0, false
	}
	// A leading zero reads as a number and writes back as a different
	// name, so the round trip rejects it.
	if deviceName(card, pcm) != name {
		return 0, 0, false
	}
	return card, pcm, true
}

// monitorID builds the pairing value from the facts that an ELD block
// and an EDID block both hold: the manufacturer, the product code,
// and the monitor name.
//
// The manufacturer and the product code are the whole of the value,
// and the name is appended only when there is one:
//
//	GSM 0x5b09 "LG ULTRAWIDE"    -> gsm-5b09-lg-ultrawide
//	BOE 0x095f ""                -> boe-095f
//
// The name is optional because the two operators must agree on every
// monitor, and one of them can read a name the other cannot. A panel
// that publishes no name descriptor gives the display operator an
// empty string, so a rule that dropped the whole value for a missing
// name would make one operator publish a pairing attribute and the
// other publish none. A claim with a matchAttribute constraint across
// the two would then park forever, with nothing in the cluster saying
// why.
//
// The name is trimmed before the spaces inside it become dashes. EDID
// pads a descriptor with spaces to fill it, so an untrimmed name
// gives a value with a dash on the end, and a monitor whose padding
// the graphics driver already stripped would not match it.
//
// An empty return means the manufacturer did not decode, and the
// caller publishes no pairing attribute at all. A constraint on a
// missing attribute allocates nothing, which is the correct answer
// for a screen this operator cannot identify.
func monitorID(manufacturer string, product uint16, name string) string {
	manufacturer = strings.ToLower(strings.TrimSpace(manufacturer))
	if manufacturer == "" {
		return ""
	}
	id := manufacturer + "-" + hexProduct(product)
	if dashedName := dashed(name); dashedName != "" {
		id += "-" + dashedName
	}
	return id
}

// hexProduct formats a monitor's product code the way the pairing
// identity and the product attribute spell it: four lowercase
// hexadecimal digits.
func hexProduct(product uint16) string {
	return fmt.Sprintf("%04x", product)
}

// dashed lowercases a monitor name and replaces each run of spaces
// with one dash.
func dashed(name string) string {
	fields := strings.Fields(strings.ToLower(name))
	return strings.Join(fields, "-")
}

// pnpID decodes the three-letter manufacturer identifier that EDID
// packs into two bytes, five bits for each letter, most significant
// letter first. The value here is the EDID's own big-endian form:
// 0x1e6d is GSM, which is LG.
//
// The kernel prints manufacture_id from an ELD block in the other
// byte order, because it reads the two bytes as a little-endian
// integer. The caller passes the bytes in EDID order, so this
// function and the display operator's own decoding agree.
func pnpID(id uint16) string {
	letters := [3]byte{
		byte((id>>10)&0x1f) + 'A' - 1,
		byte((id>>5)&0x1f) + 'A' - 1,
		byte(id&0x1f) + 'A' - 1,
	}
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return ""
		}
	}
	return string(letters[:])
}
