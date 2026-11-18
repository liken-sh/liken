package main

// A DDC/CI panel lists the VCP codes it supports in its MCCS
// capability string, and the host reads the string in fragments. The
// operator publishes only the codes of the MCCS common core, under
// readable names. It drops every manufacturer-specific code, because
// each published name is an interface that consumers can depend on,
// and no consumer uses those codes.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	vcpCapabilitiesRequest = 0xf3
	vcpCapabilitiesReply   = 0xe3
)

// A fragment is at most 32 bytes of the string. The reply data adds
// the opcode and the two offset bytes, and the frame adds the source,
// length, and checksum bytes. A display pads a reply with a short
// fragment to the full frame length.
const (
	capabilitiesFragmentBytes = 32
	capabilitiesReplyData     = capabilitiesFragmentBytes + 3
	capabilitiesReplyLength   = capabilitiesReplyData + 3
)

// capabilitiesLimit stops Capabilities on a panel that never returns
// an empty fragment.
const capabilitiesLimit = 8 << 10

// The VCP codes of the common core, besides vcpBrightness and
// vcpPowerMode, which controls.go defines.
const (
	vcpContrast    = 0x12
	vcpColorPreset = 0x14
	vcpInput       = 0x60
	vcpAudioVolume = 0x62
	vcpSharpness   = 0x87
	vcpAudioMute   = 0x8d
)

// The control names that the Display resource publishes in place of
// VCP codes.
const (
	brightnessControl  = "brightness"
	contrastControl    = "contrast"
	sharpnessControl   = "sharpness"
	colorPresetControl = "colorPreset"
	inputControl       = "input"
	audioVolumeControl = "audioVolume"
	audioMuteControl   = "audioMute"
	powerControl       = "power"
)

// coreControls maps each VCP code of the common core to its published
// name.
var coreControls = []struct {
	Code byte
	Name string
}{
	{vcpBrightness, brightnessControl},
	{vcpContrast, contrastControl},
	{vcpSharpness, sharpnessControl},
	{vcpColorPreset, colorPresetControl},
	{vcpInput, inputControl},
	{vcpAudioVolume, audioVolumeControl},
	{vcpAudioMute, audioMuteControl},
	{vcpPowerMode, powerControl},
}

// coreValues names the values of the non-continuous core codes, by
// their numbers in MCCS 2.2a. valueName publishes a value that is not
// in the table as its hexadecimal number, because a panel can accept
// values that the table does not name.
//
// The power names are PascalCase, the Kubernetes style for an enum
// value. valueRaw matches power names in any case, because
// status.captured can hold a lowercase power name that an earlier
// version of the operator wrote. Every other name must match exactly.
var coreValues = map[byte]map[uint16]string{
	vcpColorPreset: {
		0x01: "sRGB", 0x02: "native", 0x03: "4000K", 0x04: "5000K",
		0x05: "6500K", 0x06: "7500K", 0x07: "8200K", 0x08: "9300K",
		0x09: "10000K", 0x0a: "11500K", 0x0b: "user-1", 0x0c: "user-2",
		0x0d: "user-3",
	},
	vcpInput: {
		0x01: "VGA-1", 0x02: "VGA-2", 0x03: "DVI-1", 0x04: "DVI-2",
		0x05: "composite-1", 0x06: "composite-2", 0x07: "s-video-1",
		0x08: "s-video-2", 0x09: "tuner-1", 0x0a: "tuner-2", 0x0b: "tuner-3",
		0x0c: "component-1", 0x0d: "component-2", 0x0e: "component-3",
		0x0f: "DP-1", 0x10: "DP-2", 0x11: "HDMI-1", 0x12: "HDMI-2",
	},
	vcpAudioMute: {
		0x01: audioMuted, 0x02: audioUnmuted,
	},
	vcpPowerMode: {
		powerModeOn: "On", powerModeStandby: "Standby", powerModeSuspend: "Suspend",
		powerModeOff: "Off", powerModeHardOff: "HardOff",
	},
}

// The values of the audioMute control, which the spec sets as a
// boolean.
const (
	audioMuted   = "mute"
	audioUnmuted = "unmute"
)

// panelCapability describes one control of a panel: the largest value
// of a continuous control, or every value of a non-continuous control.
// The capability string lists values only for a non-continuous
// control.
type panelCapability struct {
	Max    int      `json:"max,omitempty"`
	Values []string `json:"values,omitempty"`
}

// capabilityName returns an empty string for a code outside the core.
func capabilityName(code byte) string {
	for _, control := range coreControls {
		if control.Code == code {
			return control.Name
		}
	}
	return ""
}

func valueName(code byte, raw uint16) string {
	if name, named := coreValues[code][raw]; named {
		return name
	}
	return fmt.Sprintf("%#02x", raw)
}

// valueRaw is the reverse of valueName.
func valueRaw(code byte, name string) (uint16, bool) {
	for raw, named := range coreValues[code] {
		if named == name || (code == vcpPowerMode && strings.EqualFold(named, name)) {
			return raw, true
		}
	}
	if raw, err := strconv.ParseUint(strings.TrimPrefix(name, "0x"), 16, 16); err == nil {
		return uint16(raw), true
	}
	return 0, false
}

func capabilitiesRequest(offset uint16) []byte {
	return withChecksum([]byte{
		ddcHostAddress, ddcLengthFlag | 3, vcpCapabilitiesRequest,
		byte(offset >> 8), byte(offset),
	})
}

// Capabilities reads the display's whole capability string, one
// fragment per request. An empty fragment ends the string.
func (d *DDC) Capabilities() (string, error) {
	var text strings.Builder
	for offset := 0; ; {
		fragment, err := d.capabilitiesFragment(uint16(offset))
		if err != nil {
			return "", err
		}
		if len(fragment) == 0 {
			return text.String(), nil
		}
		text.Write(fragment)
		offset += len(fragment)
		if text.Len() > capabilitiesLimit {
			return "", fmt.Errorf("the capability string is longer than the limit of %d bytes", capabilitiesLimit)
		}
	}
}

// capabilitiesFragment retries a failed exchange as GetVCP does,
// because one failed read does not show that the monitor lacks DDC/CI.
// The wait before the read doubles on each attempt, for a panel that
// replies slowly.
func (d *DDC) capabilitiesFragment(offset uint16) ([]byte, error) {
	var err error
	wait := ddcReplyDelay
	for attempt := 0; attempt < ddcGetAttempts; attempt++ {
		if attempt > 0 {
			d.sleep(ddcRetryDelay)
			wait *= 2
		}
		var fragment []byte
		fragment, err = d.capabilitiesOnce(offset, wait)
		if err == nil {
			return fragment, nil
		}
	}
	return nil, fmt.Errorf("reading the capability string at offset %d: %w", offset, err)
}

func (d *DDC) capabilitiesOnce(offset uint16, wait time.Duration) ([]byte, error) {
	if err := d.bus.Write(capabilitiesRequest(offset)); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	d.sleep(wait)
	reply := make([]byte, capabilitiesReplyLength)
	if err := d.bus.Read(reply); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoAnswer, err)
	}
	return parseCapabilitiesReply(reply, offset)
}

// parseCapabilitiesReply validates the frame as parseGetReply does.
// It also refuses a reply to another offset, because a display that
// replies to an earlier offset would repeat a fragment in the string.
func parseCapabilitiesReply(reply []byte, offset uint16) ([]byte, error) {
	if silent(reply) {
		return nil, ErrNoAnswer
	}
	if reply[0] != ddcReplySource {
		return nil, fmt.Errorf("%w: it starts with %#04x, not %#04x", ErrGarbledReply, reply[0], ddcReplySource)
	}
	length := int(reply[1] & ddcLengthMask)
	if reply[1]&ddcLengthFlag == 0 || length < 3 || length > capabilitiesReplyData {
		return nil, fmt.Errorf("%w: its length byte is %#04x", ErrGarbledReply, reply[1])
	}
	if reply[2] != vcpCapabilitiesReply {
		return nil, fmt.Errorf("%w: its opcode is %#04x, not %#04x", ErrGarbledReply, reply[2], vcpCapabilitiesReply)
	}
	body := reply[:2+length]
	if sum := ddcChecksum(ddcVirtualHost, body); sum != reply[2+length] {
		return nil, fmt.Errorf("%w: its checksum is %#04x, and the bytes add up to %#04x",
			ErrGarbledReply, reply[2+length], sum)
	}
	if replyOffset := uint16(reply[3])<<8 | uint16(reply[4]); replyOffset != offset {
		return nil, fmt.Errorf("%w: the reply has offset %d, and the request asked for %d",
			ErrGarbledReply, replyOffset, offset)
	}
	return body[5:], nil
}

// capabilityCodes parses the vcp section into the codes the panel
// supports, each with its value list. A code with no value list is a
// continuous control.
func capabilityCodes(text string) map[byte][]uint16 {
	section, found := vcpSection(text)
	if !found {
		return nil
	}
	codes := map[byte][]uint16{}
	var current byte
	named := false
	for index := 0; index < len(section); {
		character := section[index]
		switch {
		case character == '(':
			end := matchingParenthesis(section, index)
			if named {
				codes[current] = append(codes[current], hexNumbers(section[index+1:end])...)
			}
			index = end + 1
		case isHexDigit(character):
			token, next := hexToken(section, index)
			index = next
			value, err := strconv.ParseUint(token, 16, 8)
			if err != nil {
				named = false
				continue
			}
			current, named = byte(value), true
			if _, seen := codes[current]; !seen {
				codes[current] = nil
			}
		default:
			index++
		}
	}
	return codes
}

// vcpSection returns the text inside the vcp(...) section, which can
// be at any position in the string. A longer name that ends in vcp,
// such as mvcp(...), does not match.
func vcpSection(text string) (string, bool) {
	for index := 0; ; {
		found := strings.Index(text[index:], "vcp(")
		if found < 0 {
			return "", false
		}
		start := index + found
		index = start + 4
		if start > 0 && isNameCharacter(text[start-1]) {
			continue
		}
		end := matchingParenthesis(text, start+3)
		return text[start+4 : end], true
	}
}

// matchingParenthesis returns the index of the parenthesis that
// closes the one at open, or len(text) when the string ends first.
func matchingParenthesis(text string, open int) int {
	depth := 0
	for index := open; index < len(text); index++ {
		switch text[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return len(text)
}

// hexNumbers returns the hexadecimal values in one list. It skips a
// nested group, which describes a value that the operator does not
// read.
func hexNumbers(list string) []uint16 {
	var values []uint16
	for index := 0; index < len(list); {
		switch character := list[index]; {
		case character == '(':
			index = matchingParenthesis(list, index) + 1
		case isHexDigit(character):
			token, next := hexToken(list, index)
			index = next
			if value, err := strconv.ParseUint(token, 16, 16); err == nil {
				values = append(values, uint16(value))
			}
		default:
			index++
		}
	}
	return values
}

func hexToken(text string, start int) (string, int) {
	end := start
	for end < len(text) && isHexDigit(text[end]) {
		end++
	}
	return text[start:end], end
}

func isHexDigit(character byte) bool {
	return (character >= '0' && character <= '9') ||
		(character >= 'a' && character <= 'f') ||
		(character >= 'A' && character <= 'F')
}

func isNameCharacter(character byte) bool {
	return isHexDigit(character) || character == '_' ||
		(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}
