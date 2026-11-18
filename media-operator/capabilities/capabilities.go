package main

// This file turns the report of a render node's media driver into one
// boolean for each capability that a claim can select on.
//
// The report comes from the libva queries that `vainfo` also makes: the
// profiles, the entrypoints of each profile, the render-target formats
// of each pair, and the surface formats of the video processor. The
// agent decodes, encodes, and scales nothing to test them, so a driver
// that lists a profile it cannot run gets a true capability.

import (
	"slices"
)

// The VA-API values that the rules read, from libva's va.h. They are
// part of libva's ABI, so they do not change between releases.
const (
	profileNone        int32 = -1
	profileMPEG2Main   int32 = 1
	profileH264Main    int32 = 6
	profileH264High    int32 = 7
	profileVC1Advanced int32 = 10
	profileVP8         int32 = 14
	profileHEVCMain    int32 = 17
	profileHEVCMain10  int32 = 18
	profileVP9Profile0 int32 = 19
	profileVP9Profile2 int32 = 21
	profileAV1Profile0 int32 = 32

	entrypointVLD        int32 = 1
	entrypointEncSlice   int32 = 6
	entrypointEncSliceLP int32 = 8
	entrypointVideoProc  int32 = 10

	rtFormatYUV420    uint32 = 0x00000001
	rtFormatYUV420_10 uint32 = 0x00000100
)

// report is what the libva queries return for the driver of one render
// node.
type report struct {
	// Vendor is the driver's own name and version, from
	// vaQueryVendorString.
	Vendor string `json:"vendor"`
	// Configs has one entry for each profile and entrypoint pair that
	// the driver lists.
	Configs []config `json:"configs"`
	// VideoProcFormats lists the FourCC codes of the surface formats
	// that the video processor config accepts, such as NV12 and P010.
	VideoProcFormats []string `json:"videoProcFormats"`
}

// config is one profile and entrypoint pair with the render-target
// formats that the driver lists for it. RTFormat is a mask of libva's
// VA_RT_FORMAT_* bits, and is zero when the driver lists none.
type config struct {
	Profile    int32  `json:"profile"`
	Entrypoint int32  `json:"entrypoint"`
	RTFormat   uint32 `json:"rtFormat"`
}

// capabilityRule is one capability and the test of a report that
// decides whether it is true.
type capabilityRule struct {
	name  string
	check func(report) bool
}

// capabilityRules are the published capabilities. Each name is the
// device attribute that a selector reads, as
// `device.attributes["media.liken.sh"].<name>`.
//
// The H.264 rules test the High profile, because nearly every H.264
// file uses it. The VC-1 rule tests the Advanced profile, which Blu-ray
// uses. VP9 Profile 2 is 10-bit VP9. AV1 has one VA-API profile for 8
// and 10 bits, so the AV1 rules read its render-target formats.
//
// The full and the low-power encoder are separate capabilities, because
// a transcoder's settings choose one of them and a driver can list one
// without the other.
var capabilityRules = []capabilityRule{
	{"decodeH264", decodes(profileH264High, 0)},
	{"decodeHEVCMain", decodes(profileHEVCMain, 0)},
	{"decodeHEVCMain10", decodes(profileHEVCMain10, 0)},
	{"decodeAV1Main", decodes(profileAV1Profile0, rtFormatYUV420)},
	{"decodeAV1Main10", decodes(profileAV1Profile0, rtFormatYUV420_10)},
	{"decodeVP9", decodes(profileVP9Profile0, 0)},
	{"decodeVP9Profile2", decodes(profileVP9Profile2, 0)},
	{"decodeVP8", decodes(profileVP8, 0)},
	{"decodeVC1", decodes(profileVC1Advanced, 0)},
	{"decodeMPEG2", decodes(profileMPEG2Main, 0)},
	{"encodeH264", encodes(profileH264High, entrypointEncSlice)},
	{"encodeHEVCMain", encodes(profileHEVCMain, entrypointEncSlice)},
	{"encodeHEVCMain10", encodes(profileHEVCMain10, entrypointEncSlice)},
	{"encodeH264LowPower", encodes(profileH264High, entrypointEncSliceLP)},
	{"encodeHEVCMainLowPower", encodes(profileHEVCMain, entrypointEncSliceLP)},
	{"encodeHEVCMain10LowPower", encodes(profileHEVCMain10, entrypointEncSliceLP)},
	{"scale8bit", processes("NV12")},
	{"scale10bit", processes("P010")},
}

// capabilitiesOf returns every capability for one report, true or
// false, with none left out.
func capabilitiesOf(facts report) map[string]bool {
	out := make(map[string]bool, len(capabilityRules))
	for _, rule := range capabilityRules {
		out[rule.name] = rule.check(facts)
	}
	return out
}

// decodes returns a test that is true when the driver lists the profile
// with the VLD entrypoint, libva's name for full hardware decoding, and
// with every bit of format in its render-target formats.
func decodes(profile int32, format uint32) func(report) bool {
	return func(facts report) bool {
		return slices.ContainsFunc(facts.Configs, func(c config) bool {
			return c.Profile == profile && c.Entrypoint == entrypointVLD && c.RTFormat&format == format
		})
	}
}

// encodes returns a test that is true when the driver lists the profile
// with the encoder entrypoint. EncSlice is the full encoder and
// EncSliceLP the low-power one, which some Intel GPUs list alone for
// H.264 and HEVC.
func encodes(profile, entrypoint int32) func(report) bool {
	return func(facts report) bool {
		return slices.ContainsFunc(facts.Configs, func(c config) bool {
			return c.Profile == profile && c.Entrypoint == entrypoint
		})
	}
}

// processes returns a test that is true when the video processor
// accepts surfaces of the FourCC format. NV12 is 8-bit 4:2:0 and P010
// is 10-bit 4:2:0.
func processes(fourcc string) func(report) bool {
	return func(facts report) bool {
		return slices.Contains(facts.VideoProcFormats, fourcc)
	}
}
