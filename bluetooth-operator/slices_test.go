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
