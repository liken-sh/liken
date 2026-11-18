package main

// These tests compare the Route write byte for byte, because a POD of
// another shape does not reach the speaker's own volume.
// audio-operator/plans/completed/07-sinks-and-sources.md describes the
// Route write.

import (
	"encoding/json"
	"testing"
)

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
				t.Errorf("routeProps = %q, want %q", got, c.want)
			}
		})
	}
}

// The operator writes only the Output route of a device. A device with
// no Output route has no level to write.
func TestOutputRoute(t *testing.T) {
	input := json.RawMessage(`{"index": 0, "direction": "Input", "device": 1,
		"props": {"mute": false, "channelVolumes": [1.0]}}`)
	output := json.RawMessage(`{"index": 3, "direction": "Output", "device": 0,
		"props": {"mute": true, "channelVolumes": [0.5, 0.5], "volumeStep": 0.0078125}}`)
	software := json.RawMessage(`{"index": 3, "direction": "Output", "device": 0,
		"props": {"mute": false, "channelVolumes": [0.5, 0.5]}}`)

	if got := outputRoute(nil); got != nil {
		t.Errorf("a device with no routes reports %+v, want nil", got)
	}
	if got := outputRoute([]json.RawMessage{input}); got != nil {
		t.Errorf("a device with only an input route reports %+v, want nil", got)
	}
	route := outputRoute([]json.RawMessage{input, output})
	if route == nil {
		t.Fatal("outputRoute = nil, want the output route")
	}
	if route.Index != 3 || route.Device != 0 || !route.Mute || !route.AbsoluteVolume {
		t.Errorf("route = %+v, want index 3, device 0, muted, with absolute volume", route)
	}
	// A speaker with no absolute volume has no volumeStep, and a level
	// written there is a software gain.
	if got := outputRoute([]json.RawMessage{software}); got.AbsoluteVolume {
		t.Errorf("a route with no volumeStep reads as %+v, want no absolute volume", got)
	}
}
