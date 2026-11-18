package main

// The tables here decode a device's class word and its profile UUIDs
// into attribute names, so a DeviceClass can write has(input) in CEL
// instead of masking bits. The ResourceSlice publishes both raw as
// well.
//
// The class word arrives in the inquiry response during a scan. Its 24
// bits pack service flags, a major class, and a minor class. The UUIDs
// arrive from the SDP browse after pairing, one for each profile the
// device offers.

import (
	"slices"
	"strconv"
	"strings"
)

// majorClasses names the values of bits 12 to 8 of the class word that
// the Bluetooth SIG assigns.
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

// minorClasses names bits 7 to 2, which mean different things under
// each major class. Imaging is not here, because its minor bits are
// independent flags. The peripheral class splits its minor bits into
// two fields, which peripheralMinorClass decodes.
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

const peripheralMajor = 0x05

// peripheralDevices names the low four bits of a peripheral's minor
// class.
var peripheralDevices = map[uint32]string{
	0x01: "joystick",
	0x02: "gamepad",
	0x03: "remote-control",
	0x04: "sensing-device",
	0x05: "digitizer-tablet",
	0x06: "card-reader",
	0x07: "digital-pen",
}

// peripheralKeyboards names the top two bits of a peripheral's minor
// class.
var peripheralKeyboards = map[uint32]string{
	0b01: "keyboard",
	0b10: "pointing-device",
	0b11: "keyboard-pointing",
}

// serviceClasses names the flags in bits 16 to 23 of the class word.
//
// Bit 13, limited discoverable, is left out because a device sets it
// only while it is in pairing mode, and an attribute that changes on
// its own would rewrite the ResourceSlice. Bit 14, LE Audio, is left
// out because nothing selects on it.
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

// profileNames lists the profiles the ResourceSlice publishes. A
// ResourceSlice allows at most 32 attributes for each device, so the
// table names only the profiles that something selects on. Classic HID
// and HID over GATT both map to input, because a DeviceClass selects
// an input device and does not care which transport it uses.
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

// baseUUIDSuffix is the end of the Bluetooth base UUID,
// 0000xxxx-0000-1000-8000-00805f9b34fb, where xxxx is a 16-bit number
// that the SIG assigns. A vendor's UUID does not use the base.
const baseUUIDSuffix = "-0000-1000-8000-00805f9b34fb"

func majorClass(class uint32) string {
	return majorClasses[(class>>8)&0x1F]
}

// minorClass returns the name of the minor class, or the empty string
// when it has none.
func minorClass(class uint32) string {
	major := (class >> 8) & 0x1F
	minor := (class >> 2) & 0x3F
	if major == peripheralMajor {
		return peripheralMinorClass(minor)
	}
	return minorClasses[major][minor]
}

// peripheralMinorClass reads the device field first, so a gamepad with
// the keyboard bits set is still a gamepad. It uses the keyboard field
// only when the device field is zero.
func peripheralMinorClass(minor uint32) string {
	if device := minor & 0x0F; device != 0 {
		return peripheralDevices[device]
	}
	return peripheralKeyboards[(minor>>4)&0x03]
}

func serviceClassFlags(class uint32) []string {
	var flags []string
	for bit, name := range serviceClasses {
		if class&(1<<(16+uint32(bit))) != 0 {
			flags = append(flags, name)
		}
	}
	return flags
}

// profileFlags returns the sorted names of the profiles in uuids, each
// once, so the two HID UUIDs produce one input flag.
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

// shortUUID returns the 16-bit number of a UUID that uses the
// Bluetooth base, and false for any other UUID.
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
