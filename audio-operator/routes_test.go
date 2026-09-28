package main

// The Route write, byte for byte. The pod in these tests is the one
// plan 07 states, and a drifted shape is a write the speaker's own
// volume never hears.

import (
	"encoding/json"
	"testing"
)

// This string is what reaches
// spa_bt_transport_set_volume, and that every part of it is load
// bearing: the index and the device name the route, and save is false
// because the operator's own declaration is what a restart re-applies.
func TestRouteProps(t *testing.T) {
	cases := []struct {
		name    string
		route   pwRoute
		volumes []float64
		mute    *bool
		want    string
	}{
		{
			name:    "half volume on a stereo speaker",
			route:   pwRoute{Index: 1, Device: 0},
			volumes: []float64{0.5, 0.5},
			mute:    pointerTo(false),
			want:    "{ index: 1, device: 0, props: { channelVolumes: [ 0.5, 0.5 ], mute: false }, save: false }",
		},
		{
			name:    "muted",
			route:   pwRoute{Index: 2, Device: 1},
			volumes: []float64{1, 1},
			mute:    pointerTo(true),
			want:    "{ index: 2, device: 1, props: { channelVolumes: [ 1, 1 ], mute: true }, save: false }",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := routeProps(c.route, c.volumes, c.mute); got != c.want {
				t.Errorf("pod = %q, want %q", got, c.want)
			}
		})
	}
}

// A device publishes one route for each direction
// and the operator writes the output one, and that a device with no
// output route has no level for the operator to write.
func TestOutputRoute(t *testing.T) {
	input := json.RawMessage(`{"index": 0, "direction": "Input", "device": 1,
		"props": {"mute": false, "channelVolumes": [1.0]}}`)
	output := json.RawMessage(`{"index": 3, "direction": "Output", "device": 0,
		"props": {"mute": true, "channelVolumes": [0.5, 0.5], "volumeStep": 0.0078125}}`)
	software := json.RawMessage(`{"index": 3, "direction": "Output", "device": 0,
		"props": {"mute": false, "channelVolumes": [0.5, 0.5]}}`)

	if got := outputRoute(nil); got != nil {
		t.Errorf("a device with no routes reports %+v", got)
	}
	if got := outputRoute([]json.RawMessage{input}); got != nil {
		t.Errorf("a device with only an input route reports %+v", got)
	}
	route := outputRoute([]json.RawMessage{input, output})
	if route == nil {
		t.Fatal("the output route was not read")
	}
	if route.Index != 3 || route.Device != 0 || !route.Mute || !route.AbsoluteVolume {
		t.Errorf("route = %+v", route)
	}
	// A speaker with no absolute volume publishes no volumeStep, and
	// the level the operator writes there is software gain.
	if got := outputRoute([]json.RawMessage{software}); got.AbsoluteVolume {
		t.Errorf("a route with no volumeStep reports absolute volume: %+v", got)
	}
}
