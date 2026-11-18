package main

// The `axes` parameter of a claim on a controller.
//
// A resting analog stick reports noise of a step or two around its
// center. Two fields of the axis's struct input_absinfo filter it. The
// input core drops or averages a change smaller than a few times the
// fuzz, and joydev and userspace libraries such as SDL report a
// position within flat of the center as the center. systemd's hwdb
// sets both values for known controllers with the EVIOCSABS ioctl.
// This operator has no hardware database, so a claim sets them.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// absCodes maps each absolute axis name in linux/input-event-codes.h
// to its code, so a claim names an axis and not a number. ABS_MAX
// marks the end of the range and is not an axis, so it is left out.
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

// axisOverride is the fuzz and flat that a claim sets for one absolute
// axis. A nil field leaves the device's own value.
type axisOverride struct {
	Fuzz *int32 `json:"fuzz"`
	Flat *int32 `json:"flat"`
}

// axisOverrides holds a claim's values for a controller's axes, keyed
// by axis code.
type axisOverrides map[uint16]axisOverride

const axisFields = "fuzz, flat"

// parseAxisOverrides reads the `axes` block of a claim's
// configuration. Each error lists the fields an axis accepts, because
// the kubelet keeps the pod in ContainerCreating until a person fixes
// the claim from the message.
func parseAxisOverrides(block map[string]json.RawMessage) (axisOverrides, error) {
	if len(block) == 0 {
		return nil, nil
	}
	overrides := make(axisOverrides, len(block))
	for name, raw := range block {
		code, found := absCodes[name]
		if !found {
			return nil, fmt.Errorf("%q is not the name of an absolute axis. Use a kernel ABS_* code such as ABS_X or ABS_RX, with the fields %s", name, axisFields)
		}
		if code == absMTSlot {
			return nil, fmt.Errorf("ABS_MT_SLOT cannot be set, because the kernel refuses to change the number of contacts a device reserved. The other axes take the fields %s", axisFields)
		}
		var override axisOverride
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&override); err != nil {
			return nil, fmt.Errorf("reading the axis %s: %w (an axis takes the fields %s)", name, err, axisFields)
		}
		if (override.Fuzz != nil && *override.Fuzz < 0) || (override.Flat != nil && *override.Flat < 0) {
			return nil, fmt.Errorf("the axis %s has a negative value, and %s count steps of the axis, so they cannot be negative", name, axisFields)
		}
		overrides[code] = override
	}
	return overrides, nil
}

// merge combines the overrides of two claims on the same controller.
// When both set a field, it takes the larger value, so each claim gets
// at least the filtering it asked for.
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

// larger returns the greater of two optional values, or the one that
// is set.
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

// record writes the overrides as one line, which a prepared claim
// keeps so that a restarted operator applies the same values. Each
// entry is <code name>=<fuzz>:<flat>, with an empty field for a value
// that no claim set. Entries are sorted by code, so equal overrides
// always produce the same line.
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

// parseRecordedAxes reads a line that record wrote.
func parseRecordedAxes(recorded string) (axisOverrides, error) {
	if recorded == "" {
		return nil, nil
	}
	overrides := axisOverrides{}
	for _, entry := range strings.Split(recorded, ",") {
		name, values, found := strings.Cut(entry, "=")
		code, known := absCodes[name]
		if !found || !known {
			return nil, fmt.Errorf("the recorded axis %q is not in the form <code name>=<fuzz>:<flat>", entry)
		}
		fuzz, flat, found := strings.Cut(values, ":")
		if !found {
			return nil, fmt.Errorf("the recorded axis %q is not in the form <code name>=<fuzz>:<flat>", entry)
		}
		override := axisOverride{}
		var err error
		if override.Fuzz, err = recordedField(fuzz); err != nil {
			return nil, fmt.Errorf("reading the recorded fuzz of %s: %w", name, err)
		}
		if override.Flat, err = recordedField(flat); err != nil {
			return nil, fmt.Errorf("reading the recorded flat of %s: %w", name, err)
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

// absCodeName returns the name of an axis code, or its decimal number
// when absCodes has no name for it.
func absCodeName(code uint16) string {
	for name, known := range absCodes {
		if known == code {
			return name
		}
	}
	return strconv.FormatUint(uint64(code), 10)
}

// tunedAxis returns the absinfo to write to one axis. Every field
// except fuzz and flat keeps the kernel's current value. Fuzz and flat
// come from the override, and a field that it does not set goes back
// to original, the device's own value.
//
// original comes from the capability snapshot in the bond's Secret,
// which keeps the fuzz and flat the device reported before this
// operator tuned the axis. Reading the device cannot give them,
// because the kernel reports what this operator last wrote.
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

// ptr returns a pointer to value.
func ptr[T any](value T) *T { return &value }
