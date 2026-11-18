package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// axesBlock decodes raw as the `axes` value of a claim's
// configuration.
func axesBlock(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var block map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		t.Fatalf("decoding the axes block %s: %v", raw, err)
	}
	return block
}

// tuned returns an axisOverride with both fields set.
func tuned(fuzz, flat int32) axisOverride {
	return axisOverride{Fuzz: &fuzz, Flat: &flat}
}

func TestParseAxisOverrides(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    axisOverrides
		problem string
	}{
		{
			name: "the pad's noisy right stick",
			raw:  `{"ABS_RX": {"fuzz": 4}, "ABS_RY": {"fuzz": 4}}`,
			want: axisOverrides{0x03: {Fuzz: ptr(int32(4))}, 0x04: {Fuzz: ptr(int32(4))}},
		},
		{
			name: "both fields",
			raw:  `{"ABS_X": {"fuzz": 4, "flat": 15}}`,
			want: axisOverrides{0x00: tuned(4, 15)},
		},
		{
			name: "the dead zone alone",
			raw:  `{"ABS_X": {"flat": 15}}`,
			want: axisOverrides{0x00: {Flat: ptr(int32(15))}},
		},
		{
			name: "an axis with neither field",
			raw:  `{"ABS_X": {}}`,
			want: axisOverrides{0x00: {}},
		},
		{
			name:    "an unknown code name",
			raw:     `{"ABS_NOPE": {"fuzz": 4}}`,
			problem: "ABS_NOPE",
		},
		{
			name:    "a lowercase code name",
			raw:     `{"abs_rx": {"fuzz": 4}}`,
			problem: "abs_rx",
		},
		{
			name:    "a field this driver does not set",
			raw:     `{"ABS_X": {"maximum": 255}}`,
			problem: "maximum",
		},
		{
			name:    "a misspelled field",
			raw:     `{"ABS_X": {"fuz": 4}}`,
			problem: "fuz",
		},
		{
			name:    "a negative fuzz",
			raw:     `{"ABS_X": {"fuzz": -1}}`,
			problem: "negative",
		},
		{
			name:    "a negative flat",
			raw:     `{"ABS_X": {"flat": -20}}`,
			problem: "negative",
		},
		{
			// The kernel refuses EVIOCSABS on ABS_MT_SLOT.
			name:    "the multi-touch slot",
			raw:     `{"ABS_MT_SLOT": {"fuzz": 1}}`,
			problem: "ABS_MT_SLOT",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseAxisOverrides(axesBlock(t, c.raw))
			if c.problem != "" {
				if err == nil {
					t.Fatalf("parseAxisOverrides(%s) = %v, want a refusal", c.raw, got)
				}
				if !strings.Contains(err.Error(), c.problem) {
					t.Errorf("the error is %q, want it to contain %q", err, c.problem)
				}
				for _, word := range []string{"fuzz", "flat"} {
					if !strings.Contains(err.Error(), word) {
						t.Errorf("the error is %q, want it to name the field %q", err, word)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAxisOverrides(%s): %v", c.raw, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("overrides = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAxisOverridesMergeTakesTheLargestOfEach(t *testing.T) {
	cases := []struct {
		name  string
		one   axisOverrides
		other axisOverrides
		want  axisOverrides
	}{
		{
			name:  "nothing on either side",
			one:   nil,
			other: nil,
			want:  nil,
		},
		{
			name:  "one side alone",
			one:   axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
			other: nil,
			want:  axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
		},
		{
			name:  "different axes",
			one:   axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
			other: axisOverrides{0x04: {Fuzz: ptr(int32(2))}},
			want:  axisOverrides{0x03: {Fuzz: ptr(int32(4))}, 0x04: {Fuzz: ptr(int32(2))}},
		},
		{
			name:  "the same axis",
			one:   axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
			other: axisOverrides{0x03: {Fuzz: ptr(int32(8))}},
			want:  axisOverrides{0x03: {Fuzz: ptr(int32(8))}},
		},
		{
			name:  "one field from each side",
			one:   axisOverrides{0x03: {Fuzz: ptr(int32(4))}},
			other: axisOverrides{0x03: {Flat: ptr(int32(15))}},
			want:  axisOverrides{0x03: tuned(4, 15)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.one.merge(c.other); !reflect.DeepEqual(got, c.want) {
				t.Errorf("merge = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAxisOverridesRecordRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		overrides axisOverrides
		recorded  string
	}{
		{name: "nothing", overrides: nil, recorded: ""},
		{
			name:      "the pad's right stick",
			overrides: axisOverrides{0x04: {Fuzz: ptr(int32(4))}, 0x03: {Fuzz: ptr(int32(4))}},
			recorded:  "ABS_RX=4:,ABS_RY=4:",
		},
		{
			name:      "both fields",
			overrides: axisOverrides{0x00: tuned(4, 15)},
			recorded:  "ABS_X=4:15",
		},
		{
			name:      "the dead zone alone",
			overrides: axisOverrides{0x00: {Flat: ptr(int32(15))}},
			recorded:  "ABS_X=:15",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.overrides.record(); got != c.recorded {
				t.Fatalf("record = %q, want %q", got, c.recorded)
			}
			back, err := parseRecordedAxes(c.recorded)
			if err != nil {
				t.Fatalf("parseRecordedAxes(%q): %v", c.recorded, err)
			}
			if !reflect.DeepEqual(back, c.overrides) {
				t.Errorf("parseRecordedAxes(%q) = %v, want %v", c.recorded, back, c.overrides)
			}
		})
	}
}

func TestTunedAxisChangesOnlyTheSetFields(t *testing.T) {
	original := absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 0, Flat: 0, Resolution: 0}
	current := absInfo{Value: 128, Minimum: 0, Maximum: 255, Fuzz: 0, Flat: 0}

	cases := []struct {
		name     string
		override axisOverride
		want     absInfo
	}{
		{
			name:     "no claim sets this axis",
			override: axisOverride{},
			want:     current,
		},
		{
			name:     "one claim sets the fuzz",
			override: axisOverride{Fuzz: ptr(int32(4))},
			want:     absInfo{Value: 128, Minimum: 0, Maximum: 255, Fuzz: 4, Flat: 0},
		},
		{
			name:     "one claim sets both",
			override: tuned(4, 15),
			want:     absInfo{Value: 128, Minimum: 0, Maximum: 255, Fuzz: 4, Flat: 15},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tunedAxis(original, current, c.override); got != c.want {
				t.Errorf("tunedAxis = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestTunedAxisRestoresTheDevicesOwnValues(t *testing.T) {
	original := absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 1, Flat: 2}
	// The kernel reports the values this operator wrote earlier.
	current := absInfo{Value: 200, Minimum: 0, Maximum: 255, Fuzz: 4, Flat: 15}

	got := tunedAxis(original, current, axisOverride{})

	want := absInfo{Value: 200, Minimum: 0, Maximum: 255, Fuzz: 1, Flat: 2}
	if got != want {
		t.Errorf("tunedAxis = %+v, want %+v", got, want)
	}
}
