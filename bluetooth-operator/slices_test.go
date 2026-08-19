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

	devices := sliceDevices(controllers, nodes)
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

// The two taints answer two questions. NoExecute ends a session that
// a controller left. NoSchedule keeps a session from starting
// against a controller that is not there, and nobody tolerates it, so
// a claim ahead of a connect parks instead of looping through
// schedule, prepare-fail, and evict.
func TestSliceDevicesDerivesTheTwoTaints(t *testing.T) {
	cases := []struct {
		name      string
		connected bool
		nodes     []string
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
				{Key: "bluetooth.liken.sh/disconnected", Effect: "NoExecute"},
				{Key: "bluetooth.liken.sh/no-input-node", Effect: "NoSchedule"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes := map[string][]string{}
			if len(c.nodes) > 0 {
				nodes["a0:ab:51:33:b7:12"] = c.nodes
			}
			devices := sliceDevices(
				map[string]controller{"a0:ab:51:33:b7:12": {Connected: c.connected}},
				nodes,
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
	}, nil)

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
	}, map[string][]string{"a0:ab:51:33:b7:12": {"/dev/input/event5"}})

	want := map[string]any{
		"address":       "A0:AB:51:33:B7:12",
		"name":          "DualSense Wireless Controller",
		"connected":     true,
		"classOfDevice": int64(0x002508),
		"majorClass":    "peripheral",
		"minorClass":    "gamepad",
		"input":         true,
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
	)
	if got := len(*devices[0].Attributes["modalias"].String); got != 64 {
		t.Fatalf("modalias length = %d, want 64", got)
	}
}

func TestSliceDevicesTruncatesALongName(t *testing.T) {
	devices := sliceDevices(
		map[string]controller{"a0:ab:51:33:b7:12": {Name: strings.Repeat("x", 100)}},
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

func TestSameDevicesIgnoresTheServersTimestamp(t *testing.T) {
	// The API server fills TimeAdded in on every taint it stores. A
	// comparison that read it would call every pass a change, and every
	// slice write wakes every DRA-pending pod in the cluster.
	published := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	current := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute"}},
	}}
	if !sameDevices(published, current) {
		t.Fatal("a stored timestamp counted as a change")
	}
}

func TestSameDevicesSeesRealChanges(t *testing.T) {
	tainted := []SliceDevice{{
		Name:   "a0-ab-51-33-b7-12",
		Taints: []DeviceTaint{{Key: disconnectedTaint, Effect: "NoExecute", TimeAdded: "2026-08-16T12:00:00Z"}},
	}}
	clear := []SliceDevice{{Name: "a0-ab-51-33-b7-12"}}
	if sameDevices(tainted, clear) {
		t.Fatal("clearing a taint did not count as a change")
	}
	renamed := []SliceDevice{{Name: "b4-8c-9d-11-22-33"}}
	if sameDevices(clear, renamed) {
		t.Fatal("a different controller did not count as a change")
	}
}

func TestSliceName(t *testing.T) {
	// The driver name is the suffix, so liken's slice and this
	// operator's slice can both exist for one node.
	if got := sliceName("liken-1"); got != "liken-1-bluetooth.liken.sh" {
		t.Fatalf("sliceName = %q", got)
	}
}
