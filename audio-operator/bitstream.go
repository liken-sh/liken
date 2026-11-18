package main

// A film's Dolby or DTS track can reach an A/V receiver as the encoded
// bitstream, framed as IEC 61937 inside a PCM stream, and the receiver
// decodes it. That is the only way to play Dolby Atmos or DTS:X on a
// receiver's height speakers, because the decoders in a player output
// the 5.1 or 7.1 channels of the track and no height channels.
//
// PipeWire's ALSA node offers encoded formats only when both of these
// are true (enum_iec958_formats in spa/plugins/alsa/alsa-pcm.c, pipewire
// 1.6): its iec958.codecs property lists the formats, and its ALSA
// device name starts with hdmi or iec958. The name matters because for
// a bitstream the node reopens the device with the IEC 958 channel
// status bits, AES0 through AES3, as arguments, and only the hdmi and
// iec958 device definitions of alsa-lib accept them. The bits mark the
// stream as not PCM, and they reach the HDMI audio stream through the
// card's "IEC958 Playback Default" control.
//
// So the operator declares a sink whose monitor accepts encoded formats
// with the hdmi name of its PCM device and with those formats. It
// declares every other sink with the hw name, and that sink offers PCM
// only. The formats are part of the channel layout (layout.go), because
// both come from the same ELD and PipeWire reads both only from the
// drop-in.

import (
	"fmt"
	"slices"
	"strings"
)

// encodedFormat is an audio coding type of the CEA-861 short audio
// descriptors, with the name that PipeWire uses for it in
// iec958.codecs.
type encodedFormat struct {
	codingType byte
	name       string
}

// eldEncodedFormats lists the coding types that the operator passes
// through, in numeric order: the formats that PipeWire can frame as
// IEC 61937 and that films use. CEA-861 calls Dolby TrueHD
// "MLP". Dolby Atmos is carried inside E-AC-3 or TrueHD, and DTS:X
// inside DTS-HD, so neither has a coding type of its own.
var eldEncodedFormats = []encodedFormat{
	{2, "AC3"},
	{7, "DTS"},
	{10, "EAC3"},
	{11, "DTS-HD"},
	{12, "TrueHD"},
}

// encodedFormats returns the names of the formats in eldEncodedFormats
// that the monitor's descriptors list.
func (e eld) encodedFormats() []string {
	var names []string
	for _, format := range eldEncodedFormats {
		if e.EncodedTypes&(1<<format.codingType) != 0 {
			names = append(names, format.name)
		}
	}
	return names
}

// encodedTypes is the inverse of encodedFormats. It returns the coding
// type bits of the formats in names and ignores a name that is not in
// eldEncodedFormats.
func encodedTypes(names []string) uint16 {
	var types uint16
	for _, format := range eldEncodedFormats {
		if slices.Contains(names, format.name) {
			types |= 1 << format.codingType
		}
	}
	return types
}

// encodedFormatsAttribute is the device attribute that lists the
// monitor's encoded formats, separated by spaces.
const encodedFormatsAttribute = "encodedFormats"

// encodedFormatsProperty is the node property from which PipeWire's
// ALSA node reads the encoded formats. The node adds PCM to the list
// itself.
const encodedFormatsProperty = "iec958.codecs"

// hdaDriver is the driver name that every card bound to snd-hda-intel
// reports: the chipset's own codec, and the HDMI codec of an Intel, AMD,
// or NVIDIA GPU.
const hdaDriver = "HDA-Intel"

// hdaHDMIDevices maps the index of an alsa-lib hdmi device to the PCM
// device number that it opens, for an HDA-Intel card: hdmi:<card>,0
// opens PCM device 3, hdmi:<card>,1 opens PCM device 7, and so on
// (cards/HDA-Intel.conf in alsa-lib 1.2). The definition also writes the
// channel status bits to the "IEC958 Playback Default" control with the
// same index. The kernel creates one such control for each HDMI PCM
// device in this order, so a wrong entry would mark the stream of a
// different output as not PCM.
var hdaHDMIDevices = []int{3, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

// hdmiDeviceIndex returns the index of the alsa-lib hdmi device that
// opens PCM device pcm of an HDA-Intel card, and false when alsa-lib
// defines none.
func hdmiDeviceIndex(pcm int) (int, bool) {
	index := slices.Index(hdaHDMIDevices, pcm)
	return index, index >= 0
}

// passesBitstreams reports whether output can offer encoded formats:
// an HDMI or DisplayPort playback device of an HDA-Intel card, with an
// entry in hdaHDMIDevices. The operator has no such table for another
// driver, so the outputs of another driver offer PCM only.
func passesBitstreams(output alsaEndpoint) bool {
	_, defined := hdmiDeviceIndex(output.PCM)
	return defined && output.HDMI && !output.Capture && output.Identity.Driver == hdaDriver
}

// selectEncoded returns the encoded formats to declare for one sink.
// With a monitor connected, they are the formats of its ELD. Without
// one, they are the declared formats, so that switching a receiver off
// changes nothing, as in selectLayout.
func selectEncoded(output alsaEndpoint, declared channelLayout) []string {
	if !passesBitstreams(output) {
		return nil
	}
	if output.Monitor {
		return output.ELD.encodedFormats()
	}
	return declared.Encoded
}

// bitstreamArgs returns the node properties that make a sink offer
// encoded formats: the hdmi device name in place of the hw name, and the
// formats. It returns nil when the layout has no encoded formats or
// alsa-lib has no hdmi device for the PCM device.
func bitstreamArgs(card, pcm int, layout channelLayout) map[string]string {
	index, defined := hdmiDeviceIndex(pcm)
	if !defined || len(layout.Encoded) == 0 {
		return nil
	}
	return map[string]string{
		"api.alsa.path":        fmt.Sprintf("hdmi:%d,%d", card, index),
		encodedFormatsProperty: "[ " + strings.Join(layout.Encoded, " ") + " ]",
	}
}

// declaredEncoded returns the formats that bitstreamArgs wrote into
// args, or nil when it wrote none.
func declaredEncoded(args map[string]string) []string {
	formats := strings.Fields(strings.Trim(args[encodedFormatsProperty], "[] "))
	if len(formats) == 0 {
		return nil
	}
	return formats
}
