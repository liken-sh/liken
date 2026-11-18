package denon

// A Denon command such as MSMOVIE or MSDOLBY DIGITAL selects a family
// of sound modes, and the receiver then reports the mode it decodes for
// the incoming signal, such as MSDOLBY AUDIO-DD or MSDTS HD MSTR. If
// the operator compared the declared word with the reported word, it
// would find a difference while the receiver already runs the declared
// family, and send the command again each time the session checks the
// mode. A report can also contain runs of spaces, and a person can
// declare the mode in lowercase.
//
// soundModeFamilies maps each command family to the modes that the
// receiver reports while it runs that family, not counting the
// family's own word. It is the SOUND_MODE_MAPPING table of
// ol-iver/denonavr, which Home Assistant uses, with each run of spaces
// collapsed to one space:
// https://github.com/ol-iver/denonavr/blob/main/denonavr/const.py
// That table is Copyright (c) 2016 Oliver Götz, under the MIT license:
// https://github.com/ol-iver/denonavr/blob/main/LICENSE
// This table differs from the original in two ways. The AUTO and
// STANDARD rows are left out, because they contain placeholder words
// and not reported modes. MULTI CH IN and MULTI CH IN 7.1 are in both
// the Dolby and the DTS family, because the protocol specification
// lists them in both.
//
// A reported mode matches only the families that the table lists it
// under. The table lists DOLBY SURROUND under DOLBY DIGITAL only, so a
// model that selects Dolby Surround for MSMOVIE never matches a
// declared MOVIE, and the session sends MSMOVIE again each time it
// checks the mode.

import "strings"

var soundModeFamilies = map[string][]string{
	"MUSIC": {
		"AAC+NEO:X M",
		"AAC+PL2X M",
		"DOLBY D +NEO:X M",
		"DOLBY D+ +NEO:X M",
		"DOLBY D+ +PL2X M",
		"DOLBY D+NEO:X M",
		"DOLBY D+PL2X M",
		"DOLBY HD+NEO:X M",
		"DOLBY HD+PL2X M",
		"DOLBY PL2 M",
		"DOLBY PL2 MUSIC",
		"DOLBY PL2 X MUSIC",
		"DOLBY PL2X M",
		"DOLBY PLII MS",
		"DTS HD+NEO:X M",
		"DTS HD+PL2X M",
		"DTS NEO:6 M",
		"DTS NEO:6 MUSIC",
		"DTS NEO:X M",
		"DTS NEO:X MUSIC",
		"DTS+NEO:X M",
		"DTS+PL2X M",
		"M CH IN+NEO:X M",
		"M CH IN+PL2X M",
		"PLII MUSIC",
		"PLIIX MUSIC",
	},
	"MOVIE": {
		"AAC+NEO:X C",
		"AAC+PL2X C",
		"DOLBY D +NEO:X C",
		"DOLBY D+ +NEO:X C",
		"DOLBY D+ +PL2X C",
		"DOLBY D+NEO:X C",
		"DOLBY D+PL2X C",
		"DOLBY HD+NEO:X C",
		"DOLBY HD+PL2X C",
		"DOLBY PL2 C",
		"DOLBY PL2 CINEMA",
		"DOLBY PL2 MOVIE",
		"DOLBY PL2 X MOVIE",
		"DOLBY PL2X C",
		"DOLBY PLII MOVIE",
		"DOLBY PLII MV",
		"DTS HD+NEO:X C",
		"DTS HD+PL2X C",
		"DTS NEO:6 C",
		"DTS NEO:6 CINEMA",
		"DTS NEO:X C",
		"DTS NEO:X CINEMA",
		"DTS+NEO:X C",
		"DTS+PL2X C",
		"M CH IN+NEO:X C",
		"M CH IN+PL2X C",
		"MULTI IN + VIRTUAL:X",
		"PLII CINEMA",
		"PLII MOVIE",
		"PLIIX CINEMA",
	},
	"GAME": {
		"AAC+NEO:X G",
		"DOLBY D +NEO:X G",
		"DOLBY D+ +NEO:X G",
		"DOLBY D+NEO:X G",
		"DOLBY HD+NEO:X G",
		"DOLBY PL2 G",
		"DOLBY PL2 GAME",
		"DOLBY PL2 X GAME",
		"DOLBY PL2X G",
		"DOLBY PLII GAME",
		"DOLBY PLII GM",
		"DTS HD+NEO:X G",
		"DTS NEO:X G",
		"DTS+NEO:X G",
		"M CH IN+NEO:X G",
		"PLII GAME",
	},
	"VIRTUAL":         nil,
	"MATRIX":          nil,
	"ROCK ARENA":      nil,
	"JAZZ CLUB":       nil,
	"VIDEO GAME":      nil,
	"MONO MOVIE":      nil,
	"SUPER STADIUM":   nil,
	"CLASSIC CONCERT": nil,
	"WIDE SCREEN":     nil,
	"DIRECT":          nil,
	"PURE DIRECT": {
		"PURE_DIRECT",
	},
	"DOLBY DIGITAL": {
		"AAC+DOLBY EX",
		"AAC+DSUR",
		"AAC+NEURAL:X",
		"AAC+PL2Z H",
		"DOLBY ATMOS",
		"DOLBY AUDIO - DD + DSUR",
		"DOLBY AUDIO - DD + NEURAL:X",
		"DOLBY AUDIO - DD+ + DSUR",
		"DOLBY AUDIO - DD+ + NEURAL:X",
		"DOLBY AUDIO - DOLBY DIGITAL",
		"DOLBY AUDIO - DOLBY DIGITAL PLUS",
		"DOLBY AUDIO - DOLBY SURROUND",
		"DOLBY AUDIO - DOLBY TRUEHD",
		"DOLBY AUDIO - TRUEHD + DSUR",
		"DOLBY AUDIO - TRUEHD + NEURAL:X",
		"DOLBY AUDIO-DD",
		"DOLBY AUDIO-DD+",
		"DOLBY AUDIO-DD+ +DSUR",
		"DOLBY AUDIO-DD+ +NERUAL:X",
		"DOLBY AUDIO-DD+ +NEURAL:X",
		"DOLBY AUDIO-DD+DSUR",
		"DOLBY AUDIO-DD+NEURAL:X",
		"DOLBY AUDIO-DSUR",
		"DOLBY AUDIO-TRUEHD",
		"DOLBY AUDIO-TRUEHD+DSUR",
		"DOLBY AUDIO-TRUEHD+NEURAL:X",
		"DOLBY D + +DOLBY SURROUND",
		"DOLBY D + DOLBY SURROUND",
		"DOLBY D + NEURAL:X",
		"DOLBY D EX",
		"DOLBY D+",
		"DOLBY D+ +DS",
		"DOLBY D+ +EX",
		"DOLBY D+ +PL2Z H",
		"DOLBY D+DS",
		"DOLBY D+NEURAL:X",
		"DOLBY D+PL2Z H",
		"DOLBY DIGITAL +",
		"DOLBY DIGITAL + + NEURAL:X",
		"DOLBY DIGITAL + NEURAL:X",
		"DOLBY HD",
		"DOLBY HD + DOLBY SURROUND",
		"DOLBY HD+EX",
		"DOLBY HD+PL2Z H",
		"DOLBY PL2Z H",
		"DOLBY PRO LOGIC",
		"DOLBY SURROUND",
		"DOLBY TRUEHD",
		"M CH IN+DOLBY EX",
		"M CH IN+DSUR",
		"M CH IN+PL2Z H",
		"MPEG-H",
		"MPEG2 AAC",
		"MPEG4 AAC",
		"MULTI CH IN",
		"MULTI CH IN 7.1",
		"MULTI IN + DOLBY SURROUND",
		"MULTI IN + DSUR",
		"MULTI IN + NEURAL:X",
		"NEURAL",
		"STANDARD(DOLBY)",
	},
	"DTS SURROUND": {
		"AAC+NEURAL:X",
		"AAC+VIRTUAL:X",
		"DTS + DOLBY SURROUND",
		"DTS + NEURAL:X",
		"DTS + VIRTUAL:X",
		"DTS ES 8CH DSCRT",
		"DTS ES DSCRT+NEURAL:X",
		"DTS ES DSCRT6.1",
		"DTS ES MTRX+NEURAL:X",
		"DTS ES MTRX6.1",
		"DTS EXPRESS",
		"DTS HD",
		"DTS HD MSTR",
		"DTS HD+DSUR",
		"DTS HD+NEO:6",
		"DTS HD+NEURAL:X",
		"DTS HD+PL2Z H",
		"DTS HD+VIRTUAL:X",
		"DTS NEURAL:X",
		"DTS VIRTUAL:X",
		"DTS-HD",
		"DTS-HD + DOLBY SURROUND",
		"DTS-HD + DSUR",
		"DTS-HD + NEURAL:X",
		"DTS-HD MSTR",
		"DTS:X",
		"DTS:X MSTR",
		"DTS:X+VIRTUAL:X",
		"DTS+DSUR",
		"DTS+NEO:6",
		"DTS+NEUR",
		"DTS+NEURAL:X",
		"DTS+PL2Z H",
		"DTS+VIRTU",
		"DTS+VIRTUAL:X",
		"DTS96 ES MTRX",
		"DTS96/24",
		"IMAX DTS",
		"IMAX DTS:X",
		"IMAX DTS:X+VIRTUAL:X",
		"IMAX DTS+NEURAL:X",
		"IMAX DTS+VIRTUAL:X",
		"M CH IN+NEURAL:X",
		"M CH IN+VIRTUAL:X",
		"MAX DTS+VIRTUAL:X",
		"MULTI CH IN",
		"MULTI CH IN 7.1",
		"NEURAL:X",
		"STANDARD(DTS)",
		"VIRTUAL:X",
	},
	"AURO3D": {
		"AURO-3D",
	},
	"AURO2DSURR": {
		"AURO-2D SURROUND",
	},
	"MCH STEREO": {
		"MULTI CH STEREO",
		"MULTI_CH_STEREO",
	},
	"STEREO":          nil,
	"ALL ZONE STEREO": nil,
}

// soundModeFamilyOf maps each reported mode to the families that list
// it.
var soundModeFamilyOf = func() map[string][]string {
	families := map[string][]string{}
	for family, reported := range soundModeFamilies {
		for _, mode := range reported {
			families[mode] = append(families[mode], family)
		}
	}
	return families
}()

// normalSoundMode converts a mode to the table's form: upper case, with
// one space between words and none at the ends.
func normalSoundMode(mode string) string {
	return strings.Join(strings.Fields(strings.ToUpper(mode)), " ")
}

// SameSoundMode reports whether the receiver runs the declared mode:
// the two are equal after normalSoundMode, or the declared mode is a
// family that lists the reported mode. A declared mode that is not a
// family matches only itself, because the table lists no reports for
// it.
func SameSoundMode(declared, reported string) bool {
	declared, reported = normalSoundMode(declared), normalSoundMode(reported)
	if declared == reported {
		return true
	}
	for _, family := range soundModeFamilyOf[reported] {
		if family == declared {
			return true
		}
	}
	return false
}
