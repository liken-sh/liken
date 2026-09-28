package main

// These tests prove that a node's classes are the ID_INPUT_*
// properties udev would set on it. Each case is a real device's
// bitmaps, and the expected classes are what `udevadm info` reports
// for that device.

import (
	"testing"
)

// codeRange lists every code from first to last, which is how a
// keyboard's block of KEY_* codes reads in a bitmap.
func codeRange(first, last uint16) []uint16 {
	codes := make([]uint16, 0, last-first+1)
	for code := first; code <= last; code++ {
		codes = append(codes, code)
	}
	return codes
}

// The gamepad node of a DualSense as hid-playstation registers it:
// fifteen buttons, two sticks, two triggers, and the hat.
func dualSenseGamepad() evdevCapabilities {
	return evdevCapabilities{
		Name: "Wireless Controller",
		ID:   evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{
			"EV_KEY": codeRange(0x130, 0x13e),
			"EV_ABS": {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x10, 0x11},
			"EV_MSC": {0x04},
		},
	}
}

// The motion node of a DualSense. Its codes are the same ABS_X to
// ABS_RZ a stick uses; the accelerometer property is what marks it.
func dualSenseMotion() evdevCapabilities {
	return evdevCapabilities{
		Name:       "Wireless Controller Motion Sensors",
		ID:         evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Properties: []uint16{inputPropAccelerometer},
		Codes: map[string][]uint16{
			"EV_ABS": {0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
			"EV_MSC": {0x04},
		},
	}
}

// The touchpad node of a DualSense: two contacts in the multi-touch
// protocol, a click button, and the finger tool.
func dualSenseTouchpad() evdevCapabilities {
	return evdevCapabilities{
		Name: "Wireless Controller Touchpad",
		ID:   evdevID{Bus: 0x0005, Vendor: 0x054c, Product: 0x0ce6},
		Codes: map[string][]uint16{
			"EV_KEY": {0x110, 0x145, 0x14a},
			"EV_ABS": {0x00, 0x01, 0x2f, 0x35, 0x36, 0x39},
		},
	}
}

func TestNodeInputClasses(t *testing.T) {
	cases := []struct {
		name string
		caps evdevCapabilities
		want inputClasses
	}{
		{
			name: "a DualSense gamepad node",
			caps: dualSenseGamepad(),
			want: classJoystick,
		},
		{
			name: "a DualSense motion node",
			caps: dualSenseMotion(),
			want: classAccelerometer,
		},
		{
			name: "a DualSense touchpad node",
			caps: dualSenseTouchpad(),
			want: classTouchpad,
		},
		{
			// The first 32 key codes make a node a keyboard, and not
			// only a node that carries some keys.
			name: "a keyboard",
			caps: evdevCapabilities{
				Name: "Bluetooth Keyboard",
				Codes: map[string][]uint16{
					"EV_KEY": codeRange(1, 83),
					"EV_LED": {0x00, 0x01, 0x02},
					"EV_MSC": {0x04},
				},
			},
			want: classKey | classKeyboard,
		},
		{
			name: "a mouse",
			caps: evdevCapabilities{
				Name: "Bluetooth Mouse",
				Codes: map[string][]uint16{
					"EV_KEY": {0x110, 0x111, 0x112},
					"EV_REL": {0x00, 0x01, 0x06, 0x08},
				},
			},
			want: classMouse,
		},
		{
			// One node that both types and points, which is what a
			// remote with a gyroscopic cursor registers.
			name: "an air mouse",
			caps: evdevCapabilities{
				Name: "Air Remote",
				Codes: map[string][]uint16{
					"EV_KEY": append(codeRange(103, 116), 0x110, 0x111),
					"EV_REL": {0x00, 0x01, 0x08},
					"EV_MSC": {0x04},
				},
			},
			want: classKey | classMouse,
		},
		{
			// A touchscreen with one contact and no multi-touch
			// protocol is a touchscreen.
			name: "a single-touch touchscreen",
			caps: evdevCapabilities{
				Name:       "Touchscreen",
				Properties: []uint16{inputPropDirect},
				Codes: map[string][]uint16{
					"EV_KEY": {0x14a},
					"EV_ABS": {0x00, 0x01},
				},
			},
			want: classTouchscreen,
		},
		{
			name: "a pen tablet",
			caps: evdevCapabilities{
				Name: "Graphics Tablet Pen",
				Codes: map[string][]uint16{
					"EV_KEY": {0x140, 0x141, 0x14a, 0x14b, 0x14c},
					"EV_ABS": {0x00, 0x01, 0x18, 0x19, 0x1a, 0x1b},
				},
			},
			want: classTablet,
		},
		{
			// A tablet's pad node: its own buttons and ring, beside
			// the pen node of the same tablet.
			name: "a tablet pad",
			caps: evdevCapabilities{
				Name: "Graphics Tablet Pad",
				Codes: map[string][]uint16{
					"EV_KEY": {0x100, 0x101, 0x102, 0x103},
					"EV_REL": {0x08},
				},
			},
			want: classTablet | classTabletPad,
		},
		{
			name: "a lid switch",
			caps: evdevCapabilities{
				Name:  "Lid Switch",
				Codes: map[string][]uint16{"EV_SW": {0x00}},
			},
			want: classSwitch,
		},
		{
			// There is no mouse on the i2c bus, so udev reads one as
			// the pointing stick between a laptop's keys.
			name: "a pointing stick",
			caps: evdevCapabilities{
				Name: "TrackPoint",
				ID:   evdevID{Bus: busI2C},
				Codes: map[string][]uint16{
					"EV_KEY": {0x110, 0x111, 0x112},
					"EV_REL": {0x00, 0x01},
				},
			},
			want: classMouse | classPointingStick,
		},
		{
			// A node that carries only a scroll wheel is a key device.
			name: "a node with only a wheel",
			caps: evdevCapabilities{
				Name:  "Volume Wheel",
				Codes: map[string][]uint16{"EV_REL": {0x08}},
			},
			want: classKey,
		},
		{
			// Some keyboards set a stray joystick button; the
			// well-known keys tell them apart.
			name: "a keyboard with a stray joystick button",
			caps: evdevCapabilities{
				Name: "Media Keyboard",
				Codes: map[string][]uint16{
					"EV_KEY": append(codeRange(1, 83), 110, 113, 140, 144, 155, 164, 224, 0x120),
				},
			},
			want: classKey | classKeyboard,
		},
		{
			// A node with no capability this operator reads carries no
			// class.
			name: "a node with nothing on it",
			caps: evdevCapabilities{Name: "Empty"},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nodeInputClasses(c.caps); got != c.want {
				t.Errorf("classes = %s, want %s", got, c.want)
			}
		})
	}
}
