package main

// The ELD (EDID-Like Data) block is what the graphics driver writes
// into the audio driver while a monitor is connected. It is the only
// record of which PCM device plays into which monitor, and it exists
// only while the driver runs and a monitor is attached, so a walk of
// the buses cannot report it.
//
// The layout is the kernel's: snd_hdmi_parse_eld in
// sound/pci/hda/hda_eld.c reads the same bytes, and
// snd_hdmi_print_eld_info prints them into
// /proc/asound/card<N>/eld#<codec>.<pin>. The first 20 bytes are
// fixed, then come MNL bytes of monitor name, then the short audio
// descriptors of three bytes each.
//
// The operator reads the raw block because PipeWire's pa_hdmi_eld
// struct drops the manufacturer and product codes, and monitorID needs
// both to pair an audio sink with a display.

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// The size of the fixed part of the block, and the largest monitor name
// length (MNL) the kernel accepts.
const (
	eldFixedBytes = 20
	eldMaxMNL     = 16
)

// The ELD versions the kernel accepts: 2 is CEA-861D or earlier, and 31
// is a block with only the baseline fields. snd_hdmi_parse_eld rejects
// every other version, and so does parseELD, because another version
// may lay out the bytes differently.
const (
	eldVersionCEA861D = 2
	eldVersionPartial = 31
)

// The values of the two-bit connection type field. Values 2 and 3 are
// reserved, so a block with one gets an empty ConnectionType.
var eldConnectionTypes = map[byte]string{
	0: "hdmi",
	1: "displayport",
}

// The names of the speaker allocation bits, in CEA-861 order. They are
// copied from cea_speaker_allocation_names in hda_eld.c, so a value
// published here matches the one in the proc file.
var eldSpeakerNames = []string{
	"FL/FR",
	"LFE",
	"FC",
	"RL/RR",
	"RC",
	"FLC/FRC",
	"RLC/RRC",
}

// audioCodingTypeLPCM is the coding type of an uncompressed audio
// descriptor. Only LPCM descriptors set the published channel count,
// rate, and depths, because LPCM is what a workload plays through a
// PipeWire sink. Every other type goes into EncodedTypes.
const audioCodingTypeLPCM = 1

// eldSampleRates gives the rate in hertz of each bit of a descriptor's
// second byte.
var eldSampleRates = []int{32000, 44100, 48000, 88200, 96000, 176400, 192000}

// eldBitDepths lists the depth of each low bit of a descriptor's third
// byte. CEA-861 defines those bits as depths only for LPCM. For a
// compressed format the byte holds a bitrate, so parseELD reads it
// only for LPCM descriptors.
var eldBitDepths = []int{16, 20, 24}

// eld is the part of an ELD block that the operator publishes as device
// attributes. The block has more fields, such as the audio sync delay
// and the HDCP and AI flags, but none of them help a claim select an
// output.
//
// The block has no EDID serial number, so monitorID is built from the
// manufacturer, product, and name. It identifies the model of the
// monitor, and two monitors of one model get the same ID.
type eld struct {
	Version        byte
	ConnectionType string
	Manufacturer   string
	Product        uint16
	MonitorName    string
	LPCMChannels   int
	// LPCMMaxRateHz is the highest sample rate of any LPCM
	// descriptor. LPCMDepths has bit N set when any LPCM descriptor
	// supports the depth eldBitDepths[N].
	LPCMMaxRateHz int
	LPCMDepths    byte
	// Speakers is the speaker allocation as the kernel prints it, and
	// SpeakerAllocation is the same seven bits, which layout.go reads.
	Speakers          string
	SpeakerAllocation byte
	PortID            uint64
	// EncodedTypes has bit N set when a non-LPCM descriptor has coding
	// type N. bitstream.go maps the bits to format names.
	EncodedTypes uint16
}

// parseELD returns an error for a block that it cannot read, and
// alsa.go then treats the device as one with no monitor. When a
// descriptor runs past the end of the block, parseELD also returns the
// fields that it read before that descriptor.
func parseELD(raw []byte) (eld, error) {
	if len(raw) < eldFixedBytes {
		return eld{}, fmt.Errorf("the block is %d bytes, and the fixed part is %d", len(raw), eldFixedBytes)
	}

	version := raw[0] >> 3
	if version != eldVersionCEA861D && version != eldVersionPartial {
		return eld{}, fmt.Errorf("ELD version %d is not %d or %d, the versions this parser reads",
			version, eldVersionCEA861D, eldVersionPartial)
	}

	monitorNameLength := int(raw[4] & 0x1f)
	if monitorNameLength > eldMaxMNL {
		return eld{}, fmt.Errorf("the monitor name length is %d, and the block allows at most %d",
			monitorNameLength, eldMaxMNL)
	}
	if eldFixedBytes+monitorNameLength > len(raw) {
		return eld{}, fmt.Errorf("the monitor name runs past the end of a %d byte block", len(raw))
	}

	parsed := eld{
		Version:        version,
		ConnectionType: eldConnectionTypes[(raw[5]>>2)&0x3],
		// The ELD copies the EDID's manufacturer ID, which is
		// big-endian, and its product code, which is little-endian.
		Manufacturer:      pnpID(binary.BigEndian.Uint16(raw[16:18])),
		Product:           binary.LittleEndian.Uint16(raw[18:20]),
		MonitorName:       strings.TrimRight(string(raw[eldFixedBytes:eldFixedBytes+monitorNameLength]), "\x00 \t\r\n"),
		Speakers:          speakerNames(raw[7] & 0x7f),
		SpeakerAllocation: raw[7] & 0x7f,
		PortID:            binary.LittleEndian.Uint64(raw[8:16]),
	}

	// A monitor can list LPCM more than once, one descriptor per
	// channel layout, each with its own rates and depths. The
	// operator publishes the most the monitor supports: the highest
	// channel count and rate, and the union of the depths.
	descriptors := int((raw[5] >> 4) & 0xf)
	for i := range descriptors {
		start := eldFixedBytes + monitorNameLength + 3*i
		if start+3 > len(raw) {
			return parsed, fmt.Errorf("descriptor %d runs past the end of a %d byte block", i, len(raw))
		}
		descriptor := raw[start : start+3]
		if codingType := (descriptor[0] >> 3) & 0xf; codingType != audioCodingTypeLPCM {
			parsed.EncodedTypes |= 1 << codingType
			continue
		}
		if channels := int(descriptor[0]&0x7) + 1; channels > parsed.LPCMChannels {
			parsed.LPCMChannels = channels
		}
		if rate := maxSampleRate(descriptor[1]); rate > parsed.LPCMMaxRateHz {
			parsed.LPCMMaxRateHz = rate
		}
		parsed.LPCMDepths |= descriptor[2] & 0x7
	}
	return parsed, nil
}

// speakerNames returns the names of the set allocation bits, in bit
// order and separated by spaces, as the kernel prints them.
func speakerNames(allocation byte) string {
	var names []string
	for bit, name := range eldSpeakerNames {
		if allocation&(1<<bit) != 0 {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

func maxSampleRate(rates byte) int {
	highest := 0
	for bit, rate := range eldSampleRates {
		if rates&(1<<bit) != 0 {
			highest = max(highest, rate)
		}
	}
	return highest
}

// bitDepths returns the depths in LPCMDepths in ascending order,
// separated by spaces, such as "16 24".
func (e eld) bitDepths() string {
	var names []string
	for bit, depth := range eldBitDepths {
		if e.LPCMDepths&(1<<bit) != 0 {
			names = append(names, strconv.Itoa(depth))
		}
	}
	return strings.Join(names, " ")
}

func (e eld) monitorID() string {
	return monitorID(e.Manufacturer, e.Product, e.MonitorName)
}
