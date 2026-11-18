package main

import (
	"testing"
)

// codeRange returns every code from first through last, as the bitmap
// of a keyboard's block of KEY_* codes.
func codeRange(first, last uint16) []uint16 {
	codes := make([]uint16, 0, last-first+1)
	for code := first; code <= last; code++ {
		codes = append(codes, code)
	}
	return codes
}

// The gamepad node of a DualSense as hid-playstation registers it:
// buttons, two sticks, two triggers, and the hat.
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

// The motion node of a DualSense. Its axis codes are the same ABS_X to
// ABS_RZ that a stick uses, and the accelerometer property is what
// identifies it.
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

// The touchpad node of a DualSense: multi-touch axes, a click button,
// and the finger tool.
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
			// Codes 1 through 31 make a node a keyboard.
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
			// One node that both types and points, as a remote with a
			// gyroscopic cursor registers.
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
			// A single-touch screen with no multi-touch axes is still a
			// touchscreen.
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
			// A tablet's pad node, which has its own buttons and ring and
			// is separate from the tablet's pen node.
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
			// udev classes a mouse on the I2C bus as a pointing stick.
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
			name: "a node with only a wheel",
			caps: evdevCapabilities{
				Name:  "Volume Wheel",
				Codes: map[string][]uint16{"EV_REL": {0x08}},
			},
			want: classKey,
		},
		{
			// The well-known keyboard keys clear the stray joystick
			// button.
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
			name: "a node with nothing on it",
			caps: evdevCapabilities{Name: "Empty"},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nodeInputClasses(c.caps); got != c.want {
				t.Errorf("nodeInputClasses is %s, want %s", got, c.want)
			}
		})
	}
}
