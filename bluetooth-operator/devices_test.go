package main

// These tests prove what one pass publishes for each paired
// controller: the attributes a selector reads, and the taints that say
// a device cannot serve a claim right now.

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSliceDevicesPublishesPairedControllers(t *testing.T) {
	controllers := map[string]controller{
		"b4:8c:9d:11:22:33": {Name: "Player Two", Connected: false},
		"a0:ab:51:33:b7:12": {Name: "Player One", Connected: true},
	}
	nodes := map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}}

	devices := sliceDevices(controllers, nodes, nil, nil)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	// The list is sorted, so the same hardware always makes the same
	// slice and the change detection reports real changes only.
	if devices[0].Name != "a0-ab-51-33-b7-12" || devices[1].Name != "b4-8c-9d-11-22-33" {
		t.Fatalf("names = %q, %q", devices[0].Name, devices[1].Name)
	}
	if got := *devices[0].Attributes["address"].String; got != "A0:AB:51:33:B7:12" {
		t.Errorf("address = %q", got)
	}
	if got := *devices[0].Attributes["name"].String; got != "Player One" {
		t.Errorf("name = %q", got)
	}
	if got := *devices[0].Attributes["connected"].Bool; !got {
		t.Error("the connected controller published connected = false")
	}
	if len(devices[0].Taints) != 0 {
		t.Errorf("the connected controller has taints: %+v", devices[0].Taints)
	}
}

// The three taints answer three questions. NoExecute ends a session
// that a controller left. NoSchedule keeps a session from starting
// against a controller that is not there, and nobody tolerates it, so
// a claim ahead of a connect parks instead of looping through
// schedule, prepare-fail, and evict.
//
// The third taint ends a session whose container holds nodes that are
// not the ones delivered now. It goes on beside the other two, because
// a controller off the air can still have a consumer with stale nodes.
func TestSliceDevicesDerivesTheTaints(t *testing.T) {
	cases := []struct {
		name      string
		connected bool
		nodes     []string
		moved     bool
		want      []DeviceTaint
	}{
		{
			name:      "connected with an input node",
			connected: true,
			nodes:     []string{"/dev/input/event5"},
			want:      nil,
		},
		{
			name:      "disconnected",
			connected: false,
			want: []DeviceTaint{
				{Key: "bluetooth.liken.sh/disconnected", Effect: "NoExecute"},
				{Key: "bluetooth.liken.sh/no-input-node", Effect: "NoSchedule"},
			},
		},
		{
			name:      "connected with no input node yet",
			connected: true,
			want: []DeviceTaint{
				{Key: "bluetooth.liken.sh/no-input-node", Effect: "NoSchedule"},
			},
		},
		{
			name:      "a consumer holds nodes that moved",
			connected: true,
			nodes:     []string{"/dev/input/event8"},
			moved:     true,
			want: []DeviceTaint{
				{Key: "bluetooth.liken.sh/node-moved", Effect: "NoExecute"},
			},
		},
		{
			name:      "off the air with a consumer that holds nodes that moved",
			connected: false,
			nodes:     []string{"/dev/input/event8"},
			moved:     true,
			want: []DeviceTaint{
				{Key: "bluetooth.liken.sh/disconnected", Effect: "NoExecute"},
				{Key: "bluetooth.liken.sh/node-moved", Effect: "NoExecute"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes := map[string][]string{}
			if len(c.nodes) > 0 {
				nodes["a0:ab:51:33:b7:12"] = c.nodes
			}
			moved := map[string]bool{"a0:ab:51:33:b7:12": c.moved}
			devices := sliceDevices(
				map[string]controller{"a0:ab:51:33:b7:12": {Connected: c.connected}},
				nodes,
				nil,
				moved,
			)
			if !reflect.DeepEqual(devices[0].Taints, c.want) {
				t.Fatalf("taints = %+v, want %+v", devices[0].Taints, c.want)
			}
		})
	}
}

// publishedAttributes flattens one device's typed attributes into a
// plain map, so a test states its expectation as one literal.
func publishedAttributes(device SliceDevice) map[string]any {
	flat := map[string]any{}
	for name, value := range device.Attributes {
		switch {
		case value.String != nil:
			flat[name] = *value.String
		case value.Bool != nil:
			flat[name] = *value.Bool
		case value.Int != nil:
			flat[name] = *value.Int
		}
	}
	return flat
}

// The controller here is a real device: a B06+ Bluetooth audio
// receiver, publishing the exact class word and profile UUIDs it
// reported when it paired. It exercises every layer at once: the
// raw word, both unpacked class names, three service flags, and
// five profile flags.
func TestSliceDevicesPublishesTheClassAndProfileFacts(t *testing.T) {
	devices := sliceDevices(map[string]controller{
		"e3:28:e9:23:21:6f": {
			Name:        "studio-pa",
			Class:       0x2c0418,
			Modalias:    "bluetooth:v000ApFFFFdFFFF",
			Icon:        "audio-headphones",
			AddressType: "public",
			UUIDs: []string{
				fullUUID("110b"), fullUUID("110a"), fullUUID("110c"),
				fullUUID("110f"), fullUUID("1101"),
			},
		},
	}, nil, nil, nil)

	want := map[string]any{
		"address":          "E3:28:E9:23:21:6F",
		"name":             "studio-pa",
		"connected":        false,
		"classOfDevice":    int64(2884632),
		"modalias":         "bluetooth:v000ApFFFFdFFFF",
		"icon":             "audio-headphones",
		"addressType":      "public",
		"majorClass":       "audio-video",
		"minorClass":       "headphones",
		"serviceAudio":     true,
		"serviceRendering": true,
		"serviceCapturing": true,
		"audioSink":        true,
		"audioSource":      true,
		"avrcpTarget":      true,
		"avrcpController":  true,
		"serialPort":       true,
	}
	if got := publishedAttributes(devices[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %+v, want %+v", got, want)
	}
}

// A DualSense's class word sets no service flags at all, so a
// gamepad proves the flags stay absent while the major and minor
// still publish. Its input flag comes from the HID-over-GATT UUID,
// the LE transport, which must land as the same input attribute the
// classic UUID yields.
func TestSliceDevicesPublishesAGamepad(t *testing.T) {
	devices := sliceDevices(map[string]controller{
		"a0:ab:51:33:b7:12": {
			Name:      "DualSense Wireless Controller",
			Connected: true,
			Class:     0x002508,
			UUIDs:     []string{fullUUID("1812")},
		},
	},
		map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}},
		map[string]inputClasses{"a0:ab:51:33:b7:12": classJoystick | classAccelerometer | classTouchpad},
		nil,
	)

	want := map[string]any{
		"address":       "A0:AB:51:33:B7:12",
		"name":          "DualSense Wireless Controller",
		"connected":     true,
		"classOfDevice": int64(0x002508),
		"majorClass":    "peripheral",
		"minorClass":    "gamepad",
		"input":         true,
		// The three nodes a DualSense registers, as udev names them.
		// A DeviceClass selects a gamepad on `joystick` rather than on
		// the controller's name.
		"joystick":      true,
		"accelerometer": true,
		"touchpad":      true,
	}
	if got := publishedAttributes(devices[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %+v, want %+v", got, want)
	}
}

// Absent, never empty: a controller that reported no identity
// facts publishes only the three attributes every device has.
func TestSliceDevicesPublishesNothingForAnUnreportedFact(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Connected: true}},
		map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}},
		nil,
		nil,
	)

	want := map[string]any{
		"address":   "A0:AB:51:33:B7:12",
		"connected": true,
	}
	if got := publishedAttributes(devices[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %+v, want %+v", got, want)
	}
}

// Appearance is the LE counterpart of the class word, and an
// LE-only device often reports it with no class at all, so it
// publishes outside the class gate.
func TestSliceDevicesPublishesAppearance(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Appearance: 0x03C4}},
		nil,
		nil,
		nil,
	)
	if got := *devices[0].Attributes["appearance"].Int; got != 0x03C4 {
		t.Fatalf("appearance = %d, want %d", got, 0x03C4)
	}
}

// The API rejects an attribute string past 64 characters, and one
// oversized value would fail the whole slice write, so a long
// modalias takes the same cut the name takes.
func TestSliceDevicesTruncatesALongModalias(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Modalias: strings.Repeat("x", 100)}},
		nil,
		nil,
		nil,
	)
	if got := len(*devices[0].Attributes["modalias"].String); got != 64 {
		t.Fatalf("modalias length = %d, want 64", got)
	}
}

func TestSliceDevicesTruncatesALongName(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Name: strings.Repeat("x", 100)}},
		nil,
		nil,
		nil,
	)
	if got := len(*devices[0].Attributes["name"].String); got != 64 {
		t.Fatalf("name length = %d, want 64", got)
	}
}

// A person can name a controller in any script. A cut through the
// middle of a multi-byte rune is invalid UTF-8, which the API server
// rejects, and the rejection fails the whole slice write.
func TestAttributeStringCutsOnARuneBoundary(t *testing.T) {
	cases := []string{
		strings.Repeat("é", 40),       // two bytes each: 64 lands mid-rune
		strings.Repeat("あ", 40),       // three bytes each
		strings.Repeat("🎮", 40),       // four bytes each
		strings.Repeat("x", 63) + "é", // the boundary case at exactly 64
	}
	for _, name := range cases {
		truncated := attributeString(name)
		if len(truncated) > 64 {
			t.Errorf("%q truncated to %d bytes", name[:8], len(truncated))
		}
		if !utf8.ValidString(truncated) {
			t.Errorf("%q truncated to invalid UTF-8", name[:8])
		}
	}
}

// Each input class a controller's nodes carry publishes as one
// boolean attribute, named as udev names the class. A class the
// controller does not carry publishes nothing, the way every other
// absent fact does, so a selector on one guards its read.
func TestSliceDevicesPublishesOneAttributePerInputClass(t *testing.T) {
	cases := []struct {
		name    string
		classes inputClasses
		want    []string
	}{
		{name: "a gamepad", classes: classJoystick, want: []string{"joystick"}},
		{
			name:    "a keyboard",
			classes: classKey | classKeyboard,
			want:    []string{"key", "keyboard"},
		},
		{
			name:    "an air mouse",
			classes: classKey | classMouse,
			want:    []string{"key", "mouse"},
		},
		{
			name:    "every class at once",
			classes: everyInputClass,
			want: []string{
				"key", "keyboard", "mouse", "pointingstick", "touchpad", "touchscreen",
				"tablet", "tablet_pad", "joystick", "accelerometer", "switch",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			devices := sliceDevices(
				map[string]controller{"a0:ab:51:33:b7:12": {Connected: true}},
				map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}},
				map[string]inputClasses{"a0:ab:51:33:b7:12": c.classes},
				nil,
			)

			published := publishedAttributes(devices[0])
			for _, class := range c.want {
				if published[class] != true {
					t.Errorf("the device does not publish %s", class)
				}
				delete(published, class)
			}
			for _, class := range inputClassNames {
				if _, found := published[class.name]; found {
					t.Errorf("the device publishes %s, which it does not carry", class.name)
				}
			}
		})
	}
}

// A bond that has never connected has no node the relay could read,
// so it carries the profile flag from its SDP browse and no class.
// The no-input-node taint is what parks a claim on it.
func TestSliceDevicesPublishesNoClassForABondThatHasNeverConnected(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {
			Connected: true,
			UUIDs:     []string{fullUUID("1812")},
		}},
		nil,
		nil,
		nil,
	)

	want := map[string]any{
		"address":   "A0:AB:51:33:B7:12",
		"connected": true,
		"input":     true,
	}
	if got := publishedAttributes(devices[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %+v, want %+v", got, want)
	}
}
