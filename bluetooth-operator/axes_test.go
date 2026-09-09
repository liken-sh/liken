package main

// These tests prove three pure functions: reading an `axes` block,
// uniting what several claims state, and recording the result for a
// restart. None of them needs a kernel.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// axesBlock is the `axes` value of a claim's configuration, as the API
// server carries it.
func axesBlock(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var block map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &block); err != nil {
		t.Fatal(err)
	}
	return block
}

// tuned is one axis override written the way a test reads it.
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
			// A block that states neither field changes nothing, and
			// is not refused.
			name: "an axis that states nothing",
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
			// The kernel refuses a write to the slot count, because
			// the number of contacts a device reserved cannot change.
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
					t.Errorf("the refusal does not say %s: %v", c.problem, err)
				}
				// A refusal says what is accepted, so a person can fix
				// the claim from the message alone.
				for _, word := range []string{"fuzz", "flat"} {
					if !strings.Contains(err.Error(), word) {
						t.Errorf("the refusal does not name %s: %v", word, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("overrides = %v, want %v", got, c.want)
			}
		})
	}
}

// Two claims on one controller both get what they asked for, so the
// operator applies the largest value either one states, the same
// union rule the input classes take.
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

// The applied values are recorded with the input classes, so a
// restart of this operator applies the same values to the same axes.
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
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, c.overrides) {
				t.Errorf("parseRecordedAxes(%q) = %v, want %v", c.recorded, back, c.overrides)
			}
		})
	}
}

// tunedAxis is the read-modify-write systemd's hwdb entries make: the
// axis keeps everything the kernel reports, and takes the fuzz and
// the flat the claims state. An axis no claim states any more takes
// the values the device itself reported when it first connected.
func TestTunedAxisChangesOnlyTheStatedFields(t *testing.T) {
	original := absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 0, Flat: 0, Resolution: 0}
	current := absInfo{Value: 128, Minimum: 0, Maximum: 255, Fuzz: 0, Flat: 0}

	cases := []struct {
		name     string
		override axisOverride
		want     absInfo
	}{
		{
			name:     "no claim states this axis",
			override: axisOverride{},
			want:     current,
		},
		{
			name:     "one claim states the fuzz",
			override: axisOverride{Fuzz: ptr(int32(4))},
			want:     absInfo{Value: 128, Minimum: 0, Maximum: 255, Fuzz: 4, Flat: 0},
		},
		{
			name:     "one claim states both",
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

// The device's own values come back when the last claim that stated
// one goes away, and the axis keeps the position the kernel reports
// now.
func TestTunedAxisRestoresTheDevicesOwnValues(t *testing.T) {
	original := absAxis{Code: 0x03, Minimum: 0, Maximum: 255, Fuzz: 1, Flat: 2}
	// The kernel currently carries what this operator wrote.
	current := absInfo{Value: 200, Minimum: 0, Maximum: 255, Fuzz: 4, Flat: 15}

	got := tunedAxis(original, current, axisOverride{})

	want := absInfo{Value: 200, Minimum: 0, Maximum: 255, Fuzz: 1, Flat: 2}
	if got != want {
		t.Errorf("tunedAxis = %+v, want %+v", got, want)
	}
}
