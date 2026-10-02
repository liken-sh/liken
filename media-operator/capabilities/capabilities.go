package main

// The capabilities a render node's media driver advertises, as one
// boolean for each capability a workload selects on.
//
// The facts come from libva's queries, the same queries `vainfo`
// sends: the profiles the driver lists, the entrypoints of each
// profile, the render-target formats of each pair, and the surface
// formats of the video processor. The agent runs no decode, encode, or
// scale. A driver's list is the driver's statement of what it does,
// and the agent publishes that statement as it reads it. A driver that
// lists a profile it cannot run is a defect in that driver, and the
// agent does not test for it or correct it.

import (
	"slices"
)

// The VA-API values the rules read, from libva's va.h. They are part
// of libva's ABI, so they do not change between releases.
const (
	profileNone        int32 = -1
	profileH264Main    int32 = 6
	profileH264High    int32 = 7
	profileHEVCMain    int32 = 17
	profileHEVCMain10  int32 = 18
	profileVP9Profile0 int32 = 19
	profileAV1Profile0 int32 = 32

	entrypointVLD        int32 = 1
	entrypointEncSlice   int32 = 6
	entrypointEncSliceLP int32 = 8
	entrypointVideoProc  int32 = 10

	rtFormatYUV420    uint32 = 0x00000001
	rtFormatYUV420_10 uint32 = 0x00000100
)

// report is what the query reads from one render node's driver.
type report struct {
	// Vendor is the driver's own name and version, from
	// vaQueryVendorString.
	Vendor string `json:"vendor"`
	// Configs holds one entry for each profile and entrypoint pair the
	// driver lists.
	Configs []config `json:"configs"`
	// VideoProcFormats holds the FourCC codes of the surface formats
	// that the video processor's config accepts, such as NV12 and
	// P010.
	VideoProcFormats []string `json:"videoProcFormats"`
}

// config is one profile and entrypoint pair, with the render-target
// formats the driver states for it. RTFormat is a mask of libva's
// VA_RT_FORMAT_* bits, and zero when the driver states none.
type config struct {
	Profile    int32  `json:"profile"`
	Entrypoint int32  `json:"entrypoint"`
	RTFormat   uint32 `json:"rtFormat"`
}

// capabilityRule names one capability and the fact that makes it true.
type capabilityRule struct {
	name    string
	holdsIn func(report) bool
}

// capabilityRules is the published set. Each name is the attribute a
// selector reads, as `device.attributes["media.liken.sh"].<name>`.
//
// H.264 reads the High profile, because nearly every H.264 file uses
// it. AV1 has one VA-API profile for 8 and 10 bits, so its bit depth
// comes from the render-target formats of that profile.
var capabilityRules = []capabilityRule{
	{"decodeH264", decodes(profileH264High, 0)},
	{"decodeHEVCMain", decodes(profileHEVCMain, 0)},
	{"decodeHEVCMain10", decodes(profileHEVCMain10, 0)},
	{"decodeAV1Main", decodes(profileAV1Profile0, rtFormatYUV420)},
	{"decodeAV1Main10", decodes(profileAV1Profile0, rtFormatYUV420_10)},
	{"decodeVP9", decodes(profileVP9Profile0, 0)},
	{"encodeH264", encodes(profileH264High)},
	{"encodeHEVCMain", encodes(profileHEVCMain)},
	{"encodeHEVCMain10", encodes(profileHEVCMain10)},
	{"scale8bit", processes("NV12")},
	{"scale10bit", processes("P010")},
}

// capabilitiesOf states every capability for one report. Each one is
// true or false, never absent.
func capabilitiesOf(facts report) map[string]bool {
	out := make(map[string]bool, len(capabilityRules))
	for _, rule := range capabilityRules {
		out[rule.name] = rule.holdsIn(facts)
	}
	return out
}

// decodes holds when the driver lists the profile with the VLD
// entrypoint, which is libva's name for a full decoder. A nonzero
// format also requires that render-target format on that pair.
func decodes(profile int32, format uint32) func(report) bool {
	return func(facts report) bool {
		return slices.ContainsFunc(facts.Configs, func(c config) bool {
			return c.Profile == profile && c.Entrypoint == entrypointVLD && c.RTFormat&format == format
		})
	}
}

// encodes holds when the driver lists the profile with either slice
// encoder. EncSliceLP is the low-power encoder, and some Intel GPUs
// list H.264 and HEVC encode only on it.
func encodes(profile int32) func(report) bool {
	return func(facts report) bool {
		return slices.ContainsFunc(facts.Configs, func(c config) bool {
			return c.Profile == profile &&
				(c.Entrypoint == entrypointEncSlice || c.Entrypoint == entrypointEncSliceLP)
		})
	}
}

// processes holds when the video processor's config accepts surfaces
// of the format. NV12 is the 8-bit 4:2:0 format and P010 the 10-bit
// one, so a video processor that accepts P010 can scale 10-bit frames.
func processes(fourcc string) func(report) bool {
	return func(facts report) bool {
		return slices.Contains(facts.VideoProcFormats, fourcc)
	}
}
