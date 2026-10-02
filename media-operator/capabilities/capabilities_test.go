package main

import (
	"maps"
	"testing"
)

// meteorLake is the report of the Meteor Lake GPU the query read with
// the iHD driver 26.1.2, cut to the profiles the capabilities name. It
// lists encode with EncSlice, and AV1 decode at 8 and 10 bits.
func meteorLake() report {
	return report{
		Vendor: "Intel iHD driver for Intel(R) Gen Graphics - 26.1.2 ()",
		Configs: []config{
			{Profile: profileH264Main, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420},
			{Profile: profileH264High, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420},
			{Profile: profileH264High, Entrypoint: entrypointEncSlice, RTFormat: rtFormatYUV420},
			{Profile: profileHEVCMain, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420},
			{Profile: profileHEVCMain, Entrypoint: entrypointEncSlice, RTFormat: rtFormatYUV420},
			{Profile: profileHEVCMain10, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420_10},
			{Profile: profileHEVCMain10, Entrypoint: entrypointEncSlice, RTFormat: rtFormatYUV420_10},
			{Profile: profileVP9Profile0, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420},
			{Profile: profileAV1Profile0, Entrypoint: entrypointVLD, RTFormat: rtFormatYUV420 | rtFormatYUV420_10},
		},
		VideoProcFormats: []string{"NV12", "P010", "YUY2"},
	}
}

func TestTheCapabilitiesOfADriverThatAdvertisesEverything(t *testing.T) {
	got := capabilitiesOf(meteorLake())
	want := map[string]bool{
		"decodeH264":       true,
		"decodeHEVCMain":   true,
		"decodeHEVCMain10": true,
		"decodeAV1Main":    true,
		"decodeAV1Main10":  true,
		"decodeVP9":        true,
		"encodeH264":       true,
		"encodeHEVCMain":   true,
		"encodeHEVCMain10": true,
		"scale8bit":        true,
		"scale10bit":       true,
	}
	if !maps.Equal(got, want) {
		t.Errorf("capabilities = %v, want %v", got, want)
	}
}

// Each case takes one fact out of the report and names the one
// capability that must go false with it, and only that one.
func TestEachCapabilityFollowsTheFactsTheDriverAdvertises(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*report)
		falls string
	}{
		{"no H.264 High decode", dropConfig(profileH264High, entrypointVLD), "decodeH264"},
		{"no HEVC Main decode", dropConfig(profileHEVCMain, entrypointVLD), "decodeHEVCMain"},
		{"no HEVC Main 10 decode", dropConfig(profileHEVCMain10, entrypointVLD), "decodeHEVCMain10"},
		{"no VP9 profile 0 decode", dropConfig(profileVP9Profile0, entrypointVLD), "decodeVP9"},
		{"no H.264 High encode", dropConfig(profileH264High, entrypointEncSlice), "encodeH264"},
		{"no HEVC Main encode", dropConfig(profileHEVCMain, entrypointEncSlice), "encodeHEVCMain"},
		{"no HEVC Main 10 encode", dropConfig(profileHEVCMain10, entrypointEncSlice), "encodeHEVCMain10"},
		{"AV1 decode at 8 bits only", setRTFormat(profileAV1Profile0, entrypointVLD, rtFormatYUV420), "decodeAV1Main10"},
		{"AV1 decode at 10 bits only", setRTFormat(profileAV1Profile0, entrypointVLD, rtFormatYUV420_10), "decodeAV1Main"},
		{"no P010 surface on the video processor", dropFormat("P010"), "scale10bit"},
		{"no NV12 surface on the video processor", dropFormat("NV12"), "scale8bit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			facts := meteorLake()
			c.edit(&facts)
			want := capabilitiesOf(meteorLake())
			want[c.falls] = false
			if got := capabilitiesOf(facts); !maps.Equal(got, want) {
				t.Errorf("capabilities = %v, want %v", got, want)
			}
		})
	}
}

// A driver that lists encode only on the low-power entrypoint, as an
// Alder Lake GPU's iHD driver does for H.264, encodes.
func TestTheLowPowerEncoderCountsAsAnEncoder(t *testing.T) {
	facts := report{Configs: []config{
		{Profile: profileH264High, Entrypoint: entrypointEncSliceLP, RTFormat: rtFormatYUV420},
	}}
	if !capabilitiesOf(facts)["encodeH264"] {
		t.Error("EncSliceLP is an encoder entrypoint")
	}
}

// A render node that libva could not open states no fact, so every
// capability is published as false rather than left out. A selector
// that reads an absent attribute fails to evaluate, and false is what
// a claim can select against.
func TestAnEmptyReportPublishesEveryCapabilityAsFalse(t *testing.T) {
	got := capabilitiesOf(report{})
	if len(got) != len(capabilityRules) {
		t.Fatalf("capabilities = %v, want one entry for each of %d rules", got, len(capabilityRules))
	}
	for name, value := range got {
		if value {
			t.Errorf("%s = true from an empty report", name)
		}
	}
}

func dropConfig(profile, entrypoint int32) func(*report) {
	return func(r *report) {
		var kept []config
		for _, c := range r.Configs {
			if c.Profile != profile || c.Entrypoint != entrypoint {
				kept = append(kept, c)
			}
		}
		r.Configs = kept
	}
}

func setRTFormat(profile, entrypoint int32, format uint32) func(*report) {
	return func(r *report) {
		for i, c := range r.Configs {
			if c.Profile == profile && c.Entrypoint == entrypoint {
				r.Configs[i].RTFormat = format
			}
		}
	}
}

func dropFormat(fourcc string) func(*report) {
	return func(r *report) {
		var kept []string
		for _, f := range r.VideoProcFormats {
			if f != fourcc {
				kept = append(kept, f)
			}
		}
		r.VideoProcFormats = kept
	}
}
