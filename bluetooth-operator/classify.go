package main

// The decode from Bluetooth's two identity vocabularies to
// selectable attribute names.
//
// A Bluetooth device states what it is twice. The class word arrives
// in the inquiry response during a scan: 24 bits that pack a set of
// service flags, one major class, and one minor class. The UUID list
// arrives from the SDP browse after pairing, one UUID per profile
// the device advertises. The slice publishes both raw, and the
// tables here unpack them, so a DeviceClass writes has(input) in CEL
// instead of masking bits.
//
// Everything in this file is a pure function over the word and the
// list, so the vocabulary tests without a bus.

import (
	"slices"
	"strconv"
	"strings"
)

// majorClasses names bits 12 to 8 of the class word, the coarsest
// statement of what a device is. The Bluetooth SIG assigns the
// values. An unassigned value decodes to the empty string and
// publishes nothing, because the raw word already carries it.
var majorClasses = map[uint32]string{
	0x00: "miscellaneous",
	0x01: "computer",
	0x02: "phone",
	0x03: "lan-access-point",
	0x04: "audio-video",
	0x05: "peripheral",
	0x06: "imaging",
	0x07: "wearable",
	0x08: "toy",
	0x09: "health",
	0x1F: "uncategorized",
}

// minorClasses names bits 7 to 2, which mean nothing on their own:
// the same six bits read differently under each major class. The
// majors missing here have no single value to name. Imaging spreads
// its minor across independent flag bits, and the peripheral major
// splits its six bits into the two fields below.
var minorClasses = map[uint32]map[uint32]string{
	0x01: {
		0x01: "desktop",
		0x02: "server",
		0x03: "laptop",
		0x04: "handheld",
		0x05: "palm",
		0x06: "wearable",
		0x07: "tablet",
	},
	0x02: {
		0x01: "cellular",
		0x02: "cordless",
		0x03: "smartphone",
		0x04: "wired-modem",
		0x05: "isdn",
	},
	0x04: {
		0x01: "wearable-headset",
		0x02: "hands-free",
		0x04: "microphone",
		0x05: "loudspeaker",
		0x06: "headphones",
		0x07: "portable-audio",
		0x08: "car-audio",
		0x09: "set-top-box",
		0x0A: "hifi-audio",
		0x0E: "video-monitor",
		0x0F: "video-display-loudspeaker",
		0x12: "gaming",
	},
	0x07: {
		0x01: "wristwatch",
		0x02: "pager",
		0x03: "jacket",
		0x04: "helmet",
		0x05: "glasses",
	},
	0x08: {
		0x01: "robot",
		0x02: "vehicle",
		0x03: "doll",
		0x04: "controller",
		0x05: "game",
	},
	0x09: {
		0x01: "blood-pressure",
		0x02: "thermometer",
		0x03: "weighing-scale",
		0x04: "glucose",
		0x05: "pulse-oximeter",
		0x06: "heart-rate",
		0x07: "health-data-display",
	},
}

// peripheralMajor is the one major whose minor bits hold two facts
// instead of one value.
const peripheralMajor = 0x05

// peripheralDevices names the low four minor bits of a peripheral:
// the device itself.
var peripheralDevices = map[uint32]string{
	0x01: "joystick",
	0x02: "gamepad",
	0x03: "remote-control",
	0x04: "sensing-device",
	0x05: "digitizer-tablet",
	0x06: "card-reader",
	0x07: "digital-pen",
}

// peripheralKeyboards names the top two minor bits, which state
// only whether the peripheral types, points, or does both.
var peripheralKeyboards = map[uint32]string{
	0b01: "keyboard",
	0b10: "pointing-device",
	0b11: "keyboard-pointing",
}

// serviceClasses names bits 16 to 23 of the class word, one
// independent flag each; a device sets any combination. Bit 13,
// limited discoverable, is deliberately absent: it is set only
// during a pairing window, and a fact that changes on its own must
// not rewrite the slice. Bit 14, LE Audio, waits for a use to name
// it; the raw word carries it in the meantime.
var serviceClasses = [8]string{
	"servicePositioning",
	"serviceNetworking",
	"serviceRendering",
	"serviceCapturing",
	"serviceObjectTransfer",
	"serviceAudio",
	"serviceTelephony",
	"serviceInformation",
}

// profileNames is the vocabulary of profiles the slice publishes,
// not a mirror of SDP. The API allows 32 attributes per device, so a
// UUID outside this table publishes nothing until a use names it.
// Classic HID and HID over GATT collapse into the one input flag,
// because a consumer asks whether the device is an input device,
// never which transport carries the reports.
var profileNames = map[uint32]string{
	0x110B: "audioSink",
	0x110A: "audioSource",
	0x110C: "avrcpTarget",
	0x110F: "avrcpController",
	0x111E: "handsfree",
	0x1108: "headset",
	0x1124: "input",
	0x1812: "input",
	0x180F: "battery",
	0x1101: "serialPort",
}

// baseUUIDSuffix is the tail of the Bluetooth base UUID. A 16-bit
// assigned number fills characters 4 to 8 of the base, so every
// standard profile UUID differs only there, and everything outside
// the base range is a vendor's own.
const baseUUIDSuffix = "-0000-1000-8000-00805f9b34fb"

// majorClass names bits 12 to 8, or the empty string for a value
// the SIG has not assigned.
func majorClass(class uint32) string {
	return majorClasses[(class>>8)&0x1F]
}

// minorClass names bits 7 to 2 under the major that governs them,
// or the empty string when no table here reads that major.
func minorClass(class uint32) string {
	major := (class >> 8) & 0x1F
	minor := (class >> 2) & 0x3F
	if major == peripheralMajor {
		return peripheralMinorClass(minor)
	}
	return minorClasses[major][minor]
}

// peripheralMinorClass reads the device field first: a gamepad
// with the keyboard bit set is still a gamepad. The keyboard field
// answers only when the device field is zero.
func peripheralMinorClass(minor uint32) string {
	if device := minor & 0x0F; device != 0 {
		return peripheralDevices[device]
	}
	return peripheralKeyboards[(minor>>4)&0x03]
}

// serviceClassFlags names every service bit the class word sets,
// in bit order.
func serviceClassFlags(class uint32) []string {
	var flags []string
	for bit, name := range serviceClasses {
		if class&(1<<(16+uint32(bit))) != 0 {
			flags = append(flags, name)
		}
	}
	return flags
}

// profileFlags names each vocabulary profile the browsed UUIDs
// carry, once each and sorted, so the two HID UUIDs yield one input
// flag.
func profileFlags(uuids []string) []string {
	var flags []string
	for _, uuid := range uuids {
		short, ok := shortUUID(uuid)
		if !ok {
			continue
		}
		name, ok := profileNames[short]
		if !ok || slices.Contains(flags, name) {
			continue
		}
		flags = append(flags, name)
	}
	slices.Sort(flags)
	return flags
}

// shortUUID recovers the 16-bit assigned number from a UUID in the
// base range. A UUID outside that range has no assigned meaning for
// this table to name.
func shortUUID(uuid string) (uint32, bool) {
	uuid = strings.ToLower(strings.TrimSpace(uuid))
	if len(uuid) != 36 || !strings.HasPrefix(uuid, "0000") || !strings.HasSuffix(uuid, baseUUIDSuffix) {
		return 0, false
	}
	short, err := strconv.ParseUint(uuid[4:8], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint32(short), true
}
