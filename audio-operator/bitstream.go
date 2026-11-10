package main

// The encoded formats a sink passes to its monitor without decoding
// them.
//
// A film's Dolby or DTS track can reach an A/V receiver as the encoded
// bitstream, framed as IEC 61937 in a PCM stream, and the receiver
// decodes it. That is the only way a receiver plays Dolby Atmos or
// DTS:X on its height speakers, because the decoders a player has
// produce the 5.1 or 7.1 channels of the track and no height channel.
//
// PipeWire's ALSA node offers encoded formats only when two things are
// true (enum_iec958_formats in spa/plugins/alsa/alsa-pcm.c in pipewire
// 1.6): the node's iec958.codecs property lists them, and the node's
// ALSA device name starts with hdmi or iec958. The name matters
// because the node opens the device again for a bitstream with the
// IEC 958 channel status bits as arguments, AES0 through AES3, and
// only alsa-lib's hdmi and iec958 device definitions take them. The
// bits mark the stream as not PCM, and they reach the HDMI audio
// stream through the card's "IEC958 Playback Default" control.
//
// So a sink whose monitor accepts encoded formats is declared with the
// hdmi name of its PCM device and with those formats, and every other
// sink is declared with the hw name and offers PCM alone. The formats
// travel with the sink's channel layout (layout.go), because both come
// from the same ELD and both reach PipeWire only through the
// declaration.

import (
	"fmt"
	"slices"
	"strings"
)

// encodedFormat is one audio coding type of CEA-861's short audio
// descriptors, with the name PipeWire gives it in iec958.codecs.
type encodedFormat struct {
	codingType byte
	name       string
}

// eldEncodedFormats lists the coding types a sink passes through, in
// the order of their numbers. They are the five formats that PipeWire
// frames as IEC 61937 and that carry a film's sound: Dolby Digital,
// DTS, Dolby Digital Plus, DTS-HD, and Dolby TrueHD, which CEA-861
// names MLP. Dolby Atmos travels inside E-AC-3 or TrueHD, and DTS:X
// inside DTS-HD, so neither has a type of its own.
var eldEncodedFormats = []encodedFormat{
	{2, "AC3"},
	{7, "DTS"},
	{10, "EAC3"},
	{11, "DTS-HD"},
	{12, "TrueHD"},
}

// encodedFormats names the formats of eldEncodedFormats that the
// monitor's descriptors list.
func (e eld) encodedFormats() []string {
	var names []string
	for _, format := range eldEncodedFormats {
		if e.EncodedTypes&(1<<format.codingType) != 0 {
			names = append(names, format.name)
		}
	}
	return names
}

// encodedTypes is the inverse of encodedFormats: the coding type bits
// of a list of names. A name this file does not list sets no bit.
func encodedTypes(names []string) uint16 {
	var types uint16
	for _, format := range eldEncodedFormats {
		if slices.Contains(names, format.name) {
			types |= 1 << format.codingType
		}
	}
	return types
}

// encodedFormatsAttribute is the device attribute that holds the
// monitor's encoded formats, as names separated by spaces.
const encodedFormatsAttribute = "encodedFormats"

// encodedFormatsProperty is the node property that PipeWire's ALSA
// node reads the encoded formats from. The node always adds PCM to the
// list itself.
const encodedFormatsProperty = "iec958.codecs"

// hdaDriver is the driver name of every card that snd-hda-intel
// serves: the chipset's own codec, and the HDMI codec of an Intel,
// AMD, or NVIDIA GPU.
const hdaDriver = "HDA-Intel"

// hdaHDMIDevices holds the PCM device number of each hdmi device that
// alsa-lib defines for an HDA-Intel card, by the device's index:
// hdmi:<card>,0 is PCM device 3, and hdmi:<card>,1 is PCM device 7
// (cards/HDA-Intel.conf in alsa-lib 1.2). The index is also the index
// of the "IEC958 Playback Default" control that the definition writes
// the channel status bits to. The kernel creates one such control for
// each HDMI PCM device in the same order, so a table that maps the
// index to the wrong PCM device would mark a different output's stream
// as not PCM.
var hdaHDMIDevices = []int{3, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

// hdmiDeviceIndex answers the index of the alsa-lib hdmi device that
// opens one PCM device of an HDA-Intel card.
func hdmiDeviceIndex(pcm int) (int, bool) {
	index := slices.Index(hdaHDMIDevices, pcm)
	return index, index >= 0
}

// passesBitstreams reports whether the operator can declare an output
// with an hdmi device name: an HDMI or DisplayPort playback device of
// an HDA-Intel card that alsa-lib defines an hdmi device for. An
// output of any other driver is declared with its hw name and offers
// PCM alone, because this operator holds no table of that driver's
// hdmi devices.
func passesBitstreams(output alsaEndpoint) bool {
	_, defined := hdmiDeviceIndex(output.PCM)
	return defined && output.HDMI && !output.Capture && output.Identity.Driver == hdaDriver
}

// selectEncoded chooses the encoded formats one sink is declared with.
// A monitor that answers gives the formats of its ELD. A monitor that
// does not answer keeps the formats the declaration holds, for the
// reason selectLayout keeps its layout: a receiver that turns off
// changes nothing.
func selectEncoded(output alsaEndpoint, declared channelLayout) []string {
	if !passesBitstreams(output) {
		return nil
	}
	if output.Monitor {
		return output.ELD.encodedFormats()
	}
	return declared.Encoded
}

// bitstreamArgs are the properties that make a sink node offer encoded
// formats: the hdmi name of the PCM device in place of the hw name,
// and the formats. A layout with no encoded formats adds nothing, and
// neither does a PCM device that alsa-lib defines no hdmi device for.
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

// declaredEncoded reads back the formats bitstreamArgs wrote.
func declaredEncoded(args map[string]string) []string {
	formats := strings.Fields(strings.Trim(args[encodedFormatsProperty], "[] "))
	if len(formats) == 0 {
		return nil
	}
	return formats
}
