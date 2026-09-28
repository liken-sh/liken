package main

// The `axes` parameter of a claim on a controller.
//
// A stick reports position noise while it rests, one step up and one
// step down, many times a second. The input core's answer is the
// axis's fuzz: it drops a change smaller than half the fuzz before any
// handler sees it. systemd's hwdb sets fuzz and flat on known devices
// with the same EVIOCSABS ioctl, through its EVDEV_ABS_ entries. This
// driver takes the same two fields from the claim instead, because
// the operator carries no hardware database and the claim holder
// knows the pad.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// absCodes names every absolute axis the kernel defines, so a claim
// spells an axis the way linux/input-event-codes.h spells it and this
// operator never publishes a number for a person to look up. ABS_MAX
// is the limit of the range and not an axis, so it is not here.
var absCodes = map[string]uint16{
	"ABS_X":              0x00,
	"ABS_Y":              0x01,
	"ABS_Z":              0x02,
	"ABS_RX":             0x03,
	"ABS_RY":             0x04,
	"ABS_RZ":             0x05,
	"ABS_THROTTLE":       0x06,
	"ABS_RUDDER":         0x07,
	"ABS_WHEEL":          0x08,
	"ABS_GAS":            0x09,
	"ABS_BRAKE":          0x0a,
	"ABS_HAT0X":          0x10,
	"ABS_HAT0Y":          0x11,
	"ABS_HAT1X":          0x12,
	"ABS_HAT1Y":          0x13,
	"ABS_HAT2X":          0x14,
	"ABS_HAT2Y":          0x15,
	"ABS_HAT3X":          0x16,
	"ABS_HAT3Y":          0x17,
	"ABS_PRESSURE":       0x18,
	"ABS_DISTANCE":       0x19,
	"ABS_TILT_X":         0x1a,
	"ABS_TILT_Y":         0x1b,
	"ABS_TOOL_WIDTH":     0x1c,
	"ABS_VOLUME":         0x20,
	"ABS_PROFILE":        0x21,
	"ABS_SND_PROFILE":    0x22,
	"ABS_MISC":           0x28,
	"ABS_RESERVED":       0x2e,
	"ABS_MT_SLOT":        0x2f,
	"ABS_MT_TOUCH_MAJOR": 0x30,
	"ABS_MT_TOUCH_MINOR": 0x31,
	"ABS_MT_WIDTH_MAJOR": 0x32,
	"ABS_MT_WIDTH_MINOR": 0x33,
	"ABS_MT_ORIENTATION": 0x34,
	"ABS_MT_POSITION_X":  0x35,
	"ABS_MT_POSITION_Y":  0x36,
	"ABS_MT_TOOL_TYPE":   0x37,
	"ABS_MT_BLOB_ID":     0x38,
	"ABS_MT_TRACKING_ID": 0x39,
	"ABS_MT_PRESSURE":    0x3a,
	"ABS_MT_DISTANCE":    0x3b,
	"ABS_MT_TOOL_X":      0x3c,
	"ABS_MT_TOOL_Y":      0x3d,
}

// axisOverride is what a claim states about one absolute axis: the
// two fields of struct input_absinfo that a hwdb entry sets. A nil
// field states nothing, which leaves the device's own value alone.
//
// In the kernel's terms: the input core drops a change smaller than
// half the fuzz and smooths one smaller than the fuzz before any
// handler sees it, and it reports a position inside the flat around
// the axis centre as the centre.
type axisOverride struct {
	Fuzz *int32 `json:"fuzz"`
	Flat *int32 `json:"flat"`
}

// axisOverrides is what a claim states about a controller's axes,
// keyed by the axis's own code.
type axisOverrides map[uint16]axisOverride

// axisFields names what an axis block may hold, for a message that
// refuses something else.
const axisFields = "fuzz, flat"

// parseAxisOverrides reads the `axes` block of a claim's
// configuration. Every refusal names what is accepted, because the
// claim is held in ContainerCreating until a person fixes it.
func parseAxisOverrides(block map[string]json.RawMessage) (axisOverrides, error) {
	if len(block) == 0 {
		return nil, nil
	}
	overrides := make(axisOverrides, len(block))
	for name, raw := range block {
		code, found := absCodes[name]
		if !found {
			return nil, fmt.Errorf("%q is not an absolute axis; name one of the kernel's ABS_* codes, such as ABS_X or ABS_RX, and give it %s", name, axisFields)
		}
		if code == absMTSlot {
			return nil, fmt.Errorf("the kernel refuses a write to ABS_MT_SLOT, because the number of contacts a device reserved cannot change; the axes this driver sets take %s", axisFields)
		}
		var override axisOverride
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&override); err != nil {
			return nil, fmt.Errorf("reading %s: %w; an axis takes %s", name, err, axisFields)
		}
		if (override.Fuzz != nil && *override.Fuzz < 0) || (override.Flat != nil && *override.Flat < 0) {
			return nil, fmt.Errorf("%s takes no negative value; %s count the steps of the axis", name, axisFields)
		}
		overrides[code] = override
	}
	return overrides, nil
}

// merge unites what two claims state. A controller's axes take the
// largest value any prepared claim states, the same union rule the
// input classes take, so each claim receives at least the smoothing
// it asked for.
func (o axisOverrides) merge(other axisOverrides) axisOverrides {
	if len(o) == 0 && len(other) == 0 {
		return nil
	}
	united := make(axisOverrides, len(o)+len(other))
	for code, override := range o {
		united[code] = override
	}
	for code, override := range other {
		united[code] = axisOverride{
			Fuzz: larger(united[code].Fuzz, override.Fuzz),
			Flat: larger(united[code].Flat, override.Flat),
		}
	}
	return united
}

// larger is the greater of two values either of which may state
// nothing.
func larger(one, other *int32) *int32 {
	switch {
	case one == nil:
		return other
	case other == nil:
		return one
	case *other > *one:
		return other
	}
	return one
}

// record writes the applied values as one line, so a restart of this
// operator applies the same values to the same axes. Each entry is
// <code name>=<fuzz>:<flat>, with an empty field for a value the
// claims left alone, and the entries are in code order so the same
// claim always records the same line.
func (o axisOverrides) record() string {
	if len(o) == 0 {
		return ""
	}
	var entries []string
	for _, code := range slices.Sorted(maps.Keys(o)) {
		override := o[code]
		entries = append(entries, fmt.Sprintf("%s=%s:%s", absCodeName(code), field(override.Fuzz), field(override.Flat)))
	}
	return strings.Join(entries, ",")
}

func field(value *int32) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(int64(*value), 10)
}

// parseRecordedAxes reads a recorded line back.
func parseRecordedAxes(recorded string) (axisOverrides, error) {
	if recorded == "" {
		return nil, nil
	}
	overrides := axisOverrides{}
	for _, entry := range strings.Split(recorded, ",") {
		name, values, found := strings.Cut(entry, "=")
		code, known := absCodes[name]
		if !found || !known {
			return nil, fmt.Errorf("%q is not an axis record", entry)
		}
		fuzz, flat, found := strings.Cut(values, ":")
		if !found {
			return nil, fmt.Errorf("%q is not an axis record", entry)
		}
		override := axisOverride{}
		var err error
		if override.Fuzz, err = recordedField(fuzz); err != nil {
			return nil, fmt.Errorf("the fuzz of %s: %w", name, err)
		}
		if override.Flat, err = recordedField(flat); err != nil {
			return nil, fmt.Errorf("the flat of %s: %w", name, err)
		}
		overrides[code] = override
	}
	return overrides, nil
}

func recordedField(value string) (*int32, error) {
	if value == "" {
		return nil, nil
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return nil, err
	}
	return ptr(int32(number)), nil
}

// absCodeName is the kernel's name for one axis, or its number when
// the kernel defines no name for it.
func absCodeName(code uint16) string {
	for name, known := range absCodes {
		if known == code {
			return name
		}
	}
	return strconv.FormatUint(uint64(code), 10)
}

// tunedAxis is the absinfo one axis takes: everything the kernel
// reports for it now, with the fuzz and the flat the prepared claims
// state, and the device's own values for a field no claim states.
//
// The original comes from the stored snapshot, never from the device.
// The kernel reports back whatever this operator last wrote, so the
// device's own values would be lost after the first write. The
// snapshot in the bond's Secret holds them from the first time the
// controller connected.
func tunedAxis(original absAxis, current absInfo, override axisOverride) absInfo {
	tuned := current
	tuned.Fuzz, tuned.Flat = original.Fuzz, original.Flat
	if override.Fuzz != nil {
		tuned.Fuzz = *override.Fuzz
	}
	if override.Flat != nil {
		tuned.Flat = *override.Flat
	}
	return tuned
}

// ptr is the address of a value, which is how a stated field differs
// from a field the claim left out.
func ptr[T any](value T) *T { return &value }
