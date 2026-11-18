package main

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
		t.Fatalf("sliceDevices returned %d devices, want 2", len(devices))
	}
	// The devices are sorted by name, so the comparison with the
	// published slice finds only real changes.
	if devices[0].Name != "a0-ab-51-33-b7-12" || devices[1].Name != "b4-8c-9d-11-22-33" {
		t.Fatalf("device names are %q and %q, want a0-ab-51-33-b7-12 and b4-8c-9d-11-22-33", devices[0].Name, devices[1].Name)
	}
	if got := *devices[0].Attributes["address"].String; got != "A0:AB:51:33:B7:12" {
		t.Errorf("address attribute is %q, want A0:AB:51:33:B7:12", got)
	}
	if got := *devices[0].Attributes["name"].String; got != "Player One" {
		t.Errorf("name attribute is %q, want Player One", got)
	}
	if got := *devices[0].Attributes["connected"].Bool; !got {
		t.Error("the connected controller published connected = false, want true")
	}
	if len(devices[0].Taints) != 0 {
		t.Errorf("the connected controller has taints %+v, want none", devices[0].Taints)
	}
}

// The node-moved taint is set together with the disconnected taint,
// because a controller that is not connected can still have a consumer
// with stale nodes.
func TestSliceDevicesAppliesTheTaintsForEachCondition(t *testing.T) {
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
				t.Fatalf("taints are %+v, want %+v", devices[0].Taints, c.want)
			}
		})
	}
}

// publishedAttributes flattens one device's typed attributes into a
// plain map, so a test can compare it with one literal.
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

// The controller is a B06+ Bluetooth audio receiver, with the class
// word and profile UUIDs that it reported when it paired.
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
		t.Fatalf("attributes are %+v, want %+v", got, want)
	}
}

// A DualSense's class word sets no service class flags. Its `input`
// attribute comes from the HID-over-GATT UUID of Low Energy, and must
// be the same attribute that the classic HID UUID produces.
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
		"joystick":      true,
		"accelerometer": true,
		"touchpad":      true,
	}
	if got := publishedAttributes(devices[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes are %+v, want %+v", got, want)
	}
}

// A controller that BlueZ reported nothing about publishes only
// `address` and `connected`, and no attribute with an empty value.
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
		t.Fatalf("attributes are %+v, want %+v", got, want)
	}
}

// Appearance is the Low Energy counterpart of the class word. A Low
// Energy device often reports an appearance and no class word.
func TestSliceDevicesPublishesAppearance(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Appearance: 0x03C4}},
		nil,
		nil,
		nil,
	)
	if got := *devices[0].Attributes["appearance"].Int; got != 0x03C4 {
		t.Fatalf("appearance attribute is %d, want %d", got, 0x03C4)
	}
}

// The API rejects an attribute string longer than 64 bytes, and one
// rejected value fails the whole slice write.
func TestSliceDevicesTruncatesALongModalias(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Modalias: strings.Repeat("x", 100)}},
		nil,
		nil,
		nil,
	)
	if got := len(*devices[0].Attributes["modalias"].String); got != 64 {
		t.Fatalf("modalias is %d bytes, want 64", got)
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
		t.Fatalf("name is %d bytes, want 64", got)
	}
}

// A cut in the middle of a multi-byte rune is invalid UTF-8, which the
// API server rejects.
func TestAttributeStringCutsOnARuneBoundary(t *testing.T) {
	cases := []string{
		strings.Repeat("é", 40),       // two bytes each, so byte 64 is inside a rune
		strings.Repeat("あ", 40),       // three bytes each
		strings.Repeat("🎮", 40),       // four bytes each
		strings.Repeat("x", 63) + "é", // exactly 64 bytes
	}
	for _, name := range cases {
		truncated := attributeString(name)
		if len(truncated) > 64 {
			t.Errorf("%q truncated to %d bytes, want at most 64", name[:8], len(truncated))
		}
		if !utf8.ValidString(truncated) {
			t.Errorf("%q truncated to invalid UTF-8, want valid UTF-8", name[:8])
		}
	}
}

// A class that the controller does not have is not published, so a
// selector must check with has() before it reads the attribute.
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
					t.Errorf("the device lacks the %s class, want it published", class)
				}
				delete(published, class)
			}
			for _, class := range inputClassNames {
				if _, found := published[class.name]; found {
					t.Errorf("the device has the %s class, want it absent", class.name)
				}
			}
		})
	}
}

// A bond that has never connected has no node for the relay to read,
// so it has the profile flag from its UUIDs and no input class.
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
		t.Fatalf("attributes are %+v, want %+v", got, want)
	}
}
