package main

// Reads the dB range of an integer control.
//
// The number range of an integer control does not give its loudness: a
// USB DAC's 0 to 127 and a Realtek's 0 to 87 both run from silence to
// full. A driver that has the level of each step declares it in a TLV
// block, which the control interface returns on request. This file
// converts that block to the minDecibels and maxDecibels strings of
// status.capabilities.

import (
	"fmt"
	"unsafe"
)

// SNDRV_CTL_IOCTL_TLV_READ. Its argument is struct snd_ctl_tlv: the
// numid, the length of the buffer in bytes, and the buffer, all as
// 32-bit words.
var ctlIoctlTLVRead = iowr(0x1a, ctlTLVHeaderSize)

// The fixed part of struct snd_ctl_tlv, which is the numid and the
// length. The payload follows it inline.
const ctlTLVHeaderSize = 8

// The size, in 32-bit words, of the buffer that receives the TLV_READ
// reply. The longest dB block is a range of ranges, which holds one
// block for each sub-range.
const tlvPayloadWords = 256

// SNDRV_CTL_TLVD_DB_GAIN_MUTE, the value that a dB block uses for a
// step that is silence and not a level. A range whose low end is mute
// has no level to report there, so the API leaves minDecibels out.
const gainMute = -9999999

// The TLV types that carry a dB range, from include/uapi/sound/tlv.h.
// The channel-map types use the same block format but carry no level,
// so they give no range.
const (
	tlvTypeContainer    = 0
	tlvTypeDBScale      = 1
	tlvTypeDBLinear     = 2
	tlvTypeDBRange      = 3
	tlvTypeDBMinMax     = 4
	tlvTypeDBMinMaxMute = 5
)

// The mask that selects the step in the second word of a dB scale. The
// bit above it means that the minimum also mutes, which does not change
// the ends of the range.
const dbScaleStepMask = 0xffff

// decibels returns the dB range of an element, or nil when the element
// declares none.
func (m *mixer) decibels(info ctlElemInfo) *decibelRange {
	if info.Access&ctlAccessTLVRead == 0 || info.Type != ctlElemTypeInteger {
		return nil
	}
	// The buffer is a slice of 32-bit words because the kernel
	// reads the numid and the length as words and writes the payload
	// after them. A slice of words has the alignment for all three.
	buffer := make([]uint32, 2+tlvPayloadWords)
	buffer[0] = info.ID.NumID
	buffer[1] = tlvPayloadWords * 4
	if err := ioctl(m.device, ctlIoctlTLVRead, unsafe.Pointer(&buffer[0])); err != nil {
		return nil
	}
	min, max, _ := integerRange(info.Value[:])
	levels, declared := decibelRangeFrom(buffer[2:], min, max)
	if !declared {
		return nil
	}
	return &levels
}

// decibelRange is the level at the two ends of an integer control, in
// hundredths of a decibel, the unit of every dB block.
type decibelRange struct{ Low, High int32 }

// decibelRangeFrom reads a dB range from a TLV block, which is a type,
// a length in bytes, and the data.
//
// A scale gives the low end and a step for each control value, so the
// high end is the low end plus the step times (max - min). A min-max
// block and a linear block give both ends. A range of ranges and a
// container hold further blocks, and their range is the outer bounds of
// the blocks inside.
func decibelRangeFrom(payload []uint32, min, max int64) (decibelRange, bool) {
	if len(payload) < 2 {
		return decibelRange{}, false
	}
	words := int(payload[1] / 4)
	if words > len(payload)-2 {
		words = len(payload) - 2
	}
	data := payload[2 : 2+words]

	switch payload[0] {
	case tlvTypeDBScale:
		if len(data) < 2 {
			return decibelRange{}, false
		}
		low := int32(data[0])
		step := int32(data[1] & dbScaleStepMask)
		return decibelRange{Low: low, High: low + step*int32(max-min)}, true
	case tlvTypeDBLinear, tlvTypeDBMinMax, tlvTypeDBMinMaxMute:
		if len(data) < 2 {
			return decibelRange{}, false
		}
		return decibelRange{Low: int32(data[0]), High: int32(data[1])}, true
	case tlvTypeDBRange:
		return outerBounds(data, true)
	case tlvTypeContainer:
		return outerBounds(data, false)
	}
	return decibelRange{}, false
}

// outerBounds walks the blocks inside a range of ranges or a container
// and returns the widest range that they cover together.
//
// In a range of ranges (ranged is true), the two control values that a
// sub-range covers come before the sub-range's own block. In a
// container the blocks follow one another with nothing between them. A
// sub-range whose low end is mute does not set the low bound, because
// mute is not a level and would otherwise be the lowest number.
func outerBounds(data []uint32, ranged bool) (decibelRange, bool) {
	bounds := decibelRange{Low: gainMute, High: gainMute}
	found := false
	for at := 0; at+2 <= len(data); {
		min, max := int64(0), int64(0)
		if ranged {
			if at+4 > len(data) {
				break
			}
			min, max = int64(int32(data[at])), int64(int32(data[at+1]))
			at += 2
		}
		inner, declared := decibelRangeFrom(data[at:], min, max)
		if declared {
			if inner.Low != gainMute && (!found || bounds.Low == gainMute || inner.Low < bounds.Low) {
				bounds.Low = inner.Low
			}
			if inner.High != gainMute && (!found || bounds.High == gainMute || inner.High > bounds.High) {
				bounds.High = inner.High
			}
			found = true
		}
		at += 2 + int(data[at+1]/4)
	}
	return bounds, found
}

// formatDecibels writes hundredths of a decibel as a decimal string.
// The API field is a string so that it stores the exact value, because
// most hundredths, such as -0.05, have no exact binary floating-point
// form.
func formatDecibels(hundredths int32) string {
	sign := ""
	if hundredths < 0 {
		sign, hundredths = "-", -hundredths
	}
	whole, fraction := hundredths/100, hundredths%100
	switch {
	case fraction == 0:
		return fmt.Sprintf("%s%d", sign, whole)
	case fraction%10 == 0:
		return fmt.Sprintf("%s%d.%d", sign, whole, fraction/10)
	default:
		return fmt.Sprintf("%s%d.%02d", sign, whole, fraction)
	}
}
