package main

// The dB range a control declares.
//
// An integer control is a number in a range, and the number alone
// says nothing about loudness: a USB DAC's 0 to 127 and a Realtek's
// 0 to 87 are both a scale from silence to full. A card that measures
// the level behind each step declares it through TLV, a typed block
// the control interface reads on request, and this file turns that
// block into the two decimal strings status.capabilities carries at
// the range's ends.

import (
	"fmt"
	"unsafe"
)

// The request that reads a control's TLV block. Its argument is the
// numid, the length of the buffer in bytes, and the buffer after
// them, all as 32-bit words.
var ctlIoctlTLVRead = iowr(0x1a, ctlTLVHeaderSize)

// The fixed part of struct snd_ctl_tlv, the numid and the length,
// with the payload words following it inline.
const ctlTLVHeaderSize = 8

// How many words this operator gives the card to answer TLV_READ
// with. A dB range of ranges is the longest block a card writes, and
// a Realtek's, with a handful of sub-ranges, is under twenty words.
const tlvPayloadWords = 256

// The value a dB block writes where a step is silence rather than a
// level, SNDRV_CTL_TLVD_DB_GAIN_MUTE. A range whose low end is mute
// has no level to report there, so the API leaves minDecibels out.
const gainMute = -9999999

// The TLV types this operator reads a dB range out of, from
// include/uapi/sound/tlv.h. The channel-map types share the same
// block format and carry no level, so they fall through to no range.
const (
	tlvTypeContainer    = 0
	tlvTypeDBScale      = 1
	tlvTypeDBLinear     = 2
	tlvTypeDBRange      = 3
	tlvTypeDBMinMax     = 4
	tlvTypeDBMinMaxMute = 5
)

// The mask that holds a dB scale's step. The bit above it says that
// the minimum also mutes, which changes nothing about the range's
// ends.
const dbScaleStepMask = 0xffff

// decibels reads the dB range an element declares, and gives none for
// an element that declares no range.
func (m *mixer) decibels(info ctlElemInfo) *decibelRange {
	if info.Access&ctlAccessTLVRead == 0 || info.Type != ctlElemTypeInteger {
		return nil
	}
	// The buffer is a slice of words because the kernel writes the
	// numid and the length as 32-bit words and the payload after
	// them, and a slice of words is aligned for all three.
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

// decibelRange is the level at an integer control's two ends, in
// hundredths of a decibel, which is the unit every dB block writes.
type decibelRange struct{ Low, High int32 }

// decibelRangeFrom reads a dB range out of a TLV block.
//
// A block is a type, a length in bytes, and the data. The four dB
// types state their ends in three ways: a scale gives the low end
// and a step per control value, a min-max pair gives both ends, and
// a linear range gives both ends of a linear curve. A range of
// ranges and a container both hold further blocks, and the answer
// for those is the outer bounds of everything inside.
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

// outerBounds walks the blocks inside a range of ranges or a
// container and reports the widest range they cover between them.
//
// A range of ranges puts the two control values a sub-range covers
// ahead of the sub-range's own block, and a container puts the
// blocks one after another with nothing between. A sub-range whose
// low end is mute is passed over on that side, because silence is
// not a level and would otherwise be the lowest number in the walk.
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

// formatDecibels writes hundredths of a decibel as the decimal string
// the API carries. It is a string and not a number so the CRD holds
// the value exact, where a float would round -65.25 on its way
// through the API server.
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
