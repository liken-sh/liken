package main

// The MCCS capability string. A DDC/CI panel declares its whole
// feature list in one string, and the host reads it in fragments:
// one 0xf3 request per fragment, one 0xe3 reply that names the
// offset it answers. This file reads the string, parses its vcp
// section, and keeps the MCCS common core under plain names. Every
// manufacturer-specific code is dropped, because a published
// attribute is a contract, and no consumer has named one yet. The
// probe cache holds one read per panel, so a steady-state pass
// sends nothing on the wire.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The two opcodes of the capability exchange. The reply carries the
// offset it answers.
const (
	vcpCapabilitiesRequest = 0xf3
	vcpCapabilitiesReply   = 0xe3
)

// The reply frame. 32 data bytes at most, three header bytes
// (opcode and the two offset bytes), and the source, length and
// checksum around them. The display pads a short fragment, the way it
// pads a short Get reply.
const (
	capabilitiesFragmentBytes = 32
	capabilitiesReplyData     = capabilitiesFragmentBytes + 3
	capabilitiesReplyLength   = capabilitiesReplyData + 3
)

// The bound on the whole string. A panel that answers a full
// fragment forever would never end the loop, and a real string runs a
// few hundred bytes.
const capabilitiesLimit = 8 << 10

// The six VCP codes this file adds to the two that controls.go
// already names. The common core is these eight and nothing else.
const (
	vcpContrast    = 0x12
	vcpColorPreset = 0x14
	vcpInput       = 0x60
	vcpAudioVolume = 0x62
	vcpSharpness   = 0x87
	vcpAudioMute   = 0x8d
)

// The plain names the Display publishes, and why a name rather
// than a code: a person reads the resource, and the code is the
// protocol's business.
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

// The whole map between the wire and the resource. A code
// outside this list is read from no panel and published for none.
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

// The values of each non-continuous core code, in MCCS 2.2a's
// own numbering, under the names a person reads on the panel's own
// menu. A value outside the list publishes as its hexadecimal number,
// because a panel may carry a value this table does not name and the
// list has to stay honest about what the panel accepts.
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
		powerModeOn: "on", powerModeStandby: "standby", 0x03: "suspend",
		powerModeOff: "off", 0x05: "hardOff",
	},
}

// The two names of the mute control, named as constants because
// the spec's boolean maps onto them.
const (
	audioMuted   = "mute"
	audioUnmuted = "unmute"
)

// What one control carries. A continuous control states the
// largest value the panel accepts, and a non-continuous one states
// every value it accepts. The two are apart because the capability
// string states a value list for one and nothing for the other.
type panelCapability struct {
	Max    int      `json:"max,omitempty"`
	Values []string `json:"values,omitempty"`
}

// The name one core code publishes under, and the empty string
// for every other code.
func capabilityName(code byte) string {
	for _, control := range coreControls {
		if control.Code == code {
			return control.Name
		}
	}
	return ""
}

// One value under the name the resource publishes, and as a
// hexadecimal number when this file names none.
func valueName(code byte, raw uint16) string {
	if name, named := coreValues[code][raw]; named {
		return name
	}
	return fmt.Sprintf("%#02x", raw)
}

// The number behind one published value name, both directions
// of the table above.
func valueRaw(code byte, name string) (uint16, bool) {
	for raw, named := range coreValues[code] {
		if named == name {
			return raw, true
		}
	}
	if raw, err := strconv.ParseUint(strings.TrimPrefix(name, "0x"), 16, 16); err == nil {
		return uint16(raw), true
	}
	return 0, false
}

// The request packet: source, length, opcode, and the offset
// the host asks the display to continue from.
func capabilitiesRequest(offset uint16) []byte {
	return withChecksum([]byte{
		ddcHostAddress, ddcLengthFlag | 3, vcpCapabilitiesRequest,
		byte(offset >> 8), byte(offset),
	})
}

// Capabilities reads the whole string. The display answers one
// fragment per request, the host adds the fragment's length to the
// offset, and a reply with no data is the end of the string.
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
			return "", fmt.Errorf("the display's capability string passes %d bytes", capabilitiesLimit)
		}
	}
}

// The retry, for the reason GetVCP retries: one refusal says
// nothing about a monitor.
//
// The wait before each read doubles with the attempt, the same
// ladder GetVCP climbs and for the same panel.
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

// One exchange, on the timing every other message follows.
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

// The reply is checked as a frame before any byte of it is
// read, the way parseGetReply is, and the offset it answers must be
// the offset that was asked for: a display that answers an older
// offset would otherwise repeat a fragment into the string.
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
	if answered := uint16(reply[3])<<8 | uint16(reply[4]); answered != offset {
		return nil, fmt.Errorf("%w: it answers offset %d, and the request asked for %d",
			ErrGarbledReply, answered, offset)
	}
	return body[5:], nil
}

// capabilityCodes reads the vcp section: every code the panel
// carries, with the values it accepts for the codes that state a list.
// A code with no list is a continuous control.
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

// The vcp section, found by its own name rather than by
// position: a capability string states its sections in any order, and
// only this one says what the panel carries. The name must start a
// word, so a section named vcpname or mvcp is not this one.
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

// The index of the parenthesis that closes the one at open, and
// the end of the text when the string is cut short. A value list can
// hold groups of its own, so the walk counts depth.
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

// The values of one list. A group inside the list belongs to a
// value this operator does not read, so the walk skips it whole.
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

// One run of hexadecimal digits, and where the run ends.
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

// What may appear before a section name and still make it a name of
// its own, so the search finds a section and not the tail of another
// word.
func isNameCharacter(character byte) bool {
	return isHexDigit(character) || character == '_' ||
		(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

// The common core of what the panel declared, under the plain
// names. Every other code the panel carries is dropped here, and the
// resource publishes nothing about it.
func coreCapabilities(codes map[byte][]uint16) map[string]panelCapability {
	if codes == nil {
		return nil
	}
	capabilities := map[string]panelCapability{}
	for _, control := range coreControls {
		values, carried := codes[control.Code]
		if !carried {
			continue
		}
		capability := panelCapability{}
		for _, value := range values {
			capability.Values = append(capability.Values, valueName(control.Code, value))
		}
		capabilities[control.Name] = capability
	}
	return capabilities
}
