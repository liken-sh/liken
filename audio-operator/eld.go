package main

// Parsing an ELD block.
//
// The ELD, the EDID-Like Data block, is what the graphics driver
// writes into the audio driver when a monitor is connected. It is the
// only place that says which PCM device plays into which monitor, and
// a running driver is what makes it true, so no inventory walk of the
// buses can report it.
//
// The layout is the kernel's. snd_hdmi_parse_eld in
// sound/pci/hda/hda_eld.c reads the same bytes this file reads, and
// snd_hdmi_print_eld_info prints them into
// /proc/asound/card<N>/eld#<codec>.<pin> for a person. The first 20
// bytes are fixed, the monitor name follows for MNL bytes, and the
// short audio descriptors follow that, three bytes each.
//
// Two of the fields this operator publishes are in the block and not
// in what PipeWire keeps. PipeWire's pa_hdmi_eld struct holds the
// monitor name, the speaker allocation, the IEC958 codec list, and
// the LPCM channel count, and drops the manufacturer and product
// codes. Those two are half of the pairing identity, so the operator
// reads the raw block itself.

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// The fixed part of the block, and the limits the kernel enforces on
// what follows it.
const (
	eldFixedBytes = 20
	eldMaxMNL     = 16
)

// The two ELD versions the kernel accepts. Version 2 is CEA-861D or
// below, and version 31 says the block carries the baseline fields
// only. snd_hdmi_parse_eld rejects every other value, and so does
// this parser, because a version it does not know may lay the bytes
// out differently.
const (
	eldVersionCEA861D = 2
	eldVersionPartial = 31
)

// The connection types the two-bit field names. Values 2 and 3 are
// reserved, and a block that carries one publishes no connection
// type.
var eldConnectionTypes = map[byte]string{
	0: "hdmi",
	1: "displayport",
}

// The speaker allocation bits, in CEA-861 order. The names are the
// kernel's own, from cea_speaker_allocation_names in hda_eld.c, so a
// value published here reads the same as the one in the proc file.
var eldSpeakerNames = []string{
	"FL/FR",
	"LFE",
	"FC",
	"RL/RR",
	"RC",
	"FLC/FRC",
	"RLC/RRC",
}

// audioCodingTypeLPCM is the short audio descriptor format that
// carries uncompressed stereo and multichannel audio. It is the only
// format whose channel count this operator publishes, because it is
// the one a workload plays through a PipeWire sink.
const audioCodingTypeLPCM = 1

// eld holds what this operator publishes out of one block. The block
// carries more, including the audio sync delay, the HDCP and AI
// flags, and one descriptor for each compressed format the monitor
// accepts. None of those select an output.
//
// The block carries no serial number. snd_hdmi_print_eld_info prints
// every field, and there is no EDID serial among them, so an audio
// device says which model of monitor it plays into and cannot say
// which unit. That limit is why the pairing attribute is built from
// the manufacturer, the product, and the name.
type eld struct {
	Version        byte
	ConnectionType string
	Manufacturer   string
	Product        uint16
	MonitorName    string
	LPCMChannels   int
	Speakers       string
	PortID         uint64
}

// parseELD reads one raw block. A block that is too short, or that
// carries a version this parser does not know, is an error: the
// caller treats the output as one whose monitor it cannot identify,
// which is the same handling an absent monitor gets.
func parseELD(raw []byte) (eld, error) {
	if len(raw) < eldFixedBytes {
		return eld{}, fmt.Errorf("the block is %d bytes, and the fixed part is %d", len(raw), eldFixedBytes)
	}

	version := raw[0] >> 3
	if version != eldVersionCEA861D && version != eldVersionPartial {
		return eld{}, fmt.Errorf("ELD version %d is not one this parser reads", version)
	}

	monitorNameLength := int(raw[4] & 0x1f)
	if monitorNameLength > eldMaxMNL {
		return eld{}, fmt.Errorf("the monitor name length %d is a reserved value", monitorNameLength)
	}
	if eldFixedBytes+monitorNameLength > len(raw) {
		return eld{}, fmt.Errorf("the monitor name runs past the end of a %d byte block", len(raw))
	}

	parsed := eld{
		Version:        version,
		ConnectionType: eldConnectionTypes[(raw[5]>>2)&0x3],
		// The manufacturer bytes are the EDID's own two bytes, copied
		// into the block in EDID order, so the decoding reads them
		// big-endian. The product code is little-endian in EDID and
		// stays that way here.
		Manufacturer: pnpID(binary.BigEndian.Uint16(raw[16:18])),
		Product:      binary.LittleEndian.Uint16(raw[18:20]),
		MonitorName:  strings.TrimRight(string(raw[eldFixedBytes:eldFixedBytes+monitorNameLength]), "\x00 \t\r\n"),
		Speakers:     speakerNames(raw[7] & 0x7f),
		PortID:       binary.LittleEndian.Uint64(raw[8:16]),
	}

	// The channel count comes from the LPCM descriptor. A monitor may
	// publish several descriptors, and the highest LPCM count is the
	// one a workload can actually use.
	descriptors := int((raw[5] >> 4) & 0xf)
	for i := range descriptors {
		start := eldFixedBytes + monitorNameLength + 3*i
		if start+3 > len(raw) {
			return parsed, fmt.Errorf("descriptor %d runs past the end of a %d byte block", i, len(raw))
		}
		descriptor := raw[start : start+3]
		if (descriptor[0]>>3)&0xf != audioCodingTypeLPCM {
			continue
		}
		if channels := int(descriptor[0]&0x7) + 1; channels > parsed.LPCMChannels {
			parsed.LPCMChannels = channels
		}
	}
	return parsed, nil
}

// speakerNames turns the speaker allocation bitmap into the names the
// kernel prints for it, in bit order, separated by spaces. A monitor
// with stereo speakers gives "FL/FR".
func speakerNames(allocation byte) string {
	var names []string
	for bit, name := range eldSpeakerNames {
		if allocation&(1<<bit) != 0 {
			names = append(names, name)
		}
	}
	return strings.Join(names, " ")
}

// monitorID builds the pairing value for this block.
func (e eld) monitorID() string {
	return monitorID(e.Manufacturer, e.Product, e.MonitorName)
}
