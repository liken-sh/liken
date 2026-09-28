package main

// The Route on a Bluetooth device, which is where a speaker's own
// volume lives.
//
// A Bluetooth speaker is two objects in the graph: a device, which
// WirePlumber's bluez monitor builds from the BlueZ transport, and a
// node, which plays into it. The device publishes a Route for each
// direction of its profile, and the Route's props carry the level.
// A level written on the device's Route reaches
// spa_bt_transport_set_volume in the bluez5 plugin, which writes the
// Volume property on org.bluez.MediaTransport1, and that is AVRCP
// absolute volume: the number on the speaker's own display moves.
// The same level written on the node is a software gain and nothing
// more, because the A2DP follower accepts no volume key
// (spa/plugins/bluez5/media-sink.c in pipewire 1.4.2).
//
// So spec.volume on a speaker writes the Route. The Route's read-only
// volumeStep is how the graph says the transport reports a volume at
// all, and a speaker without one gets the software gain, like an
// ALSA endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
)

// A device publishes one route for each direction of its current
// profile. Output is the speaker's, and the one direction this pod
// plays.
const outputRouteDirection = "Output"

// pwRoute is what one Route carries. The index and the profile device
// address the route in a write, and the props hold the level.
type pwRoute struct {
	Index   int
	Device  int
	Mute    bool
	Volumes []float64
	// AbsoluteVolume says the transport reports a volume of its own.
	// The graph shows it as a read-only volumeStep, which is
	// 1/(max+1) of the peer's own scale, and a speaker with no
	// absolute volume publishes none.
	AbsoluteVolume bool
}

// routeParam is the shape pw-dump prints a Route in. This struct
// reads the four fields a level write and the status need, and
// leaves the name, the priority, and the profile list unparsed.
type routeParam struct {
	Index     int    `json:"index"`
	Direction string `json:"direction"`
	Device    int    `json:"device"`
	Props     struct {
		Mute           bool      `json:"mute"`
		ChannelVolumes []float64 `json:"channelVolumes"`
		VolumeStep     *float64  `json:"volumeStep"`
	} `json:"props"`
}

// outputRoute picks the speaker's route out of a device's Route
// params. A device with no output route is a device with no level to
// write, and the operator reports none rather than writing to index
// zero.
func outputRoute(params []json.RawMessage) *pwRoute {
	for _, raw := range params {
		var param routeParam
		if err := json.Unmarshal(raw, &param); err != nil {
			continue
		}
		if param.Direction != outputRouteDirection {
			continue
		}
		return &pwRoute{
			Index:          param.Index,
			Device:         param.Device,
			Mute:           param.Props.Mute,
			Volumes:        param.Props.ChannelVolumes,
			AbsoluteVolume: param.Props.VolumeStep != nil,
		}
	}
	return nil
}

// routeProps is the pod a Route write takes. The index and the device
// name the route, the props carry the level, and save is false
// because WirePlumber stores nothing in this pod: the operator's own
// declaration is what a restart re-applies.
func routeProps(route pwRoute, volumes []float64, mute *bool) string {
	return fmt.Sprintf("{ index: %d, device: %d, props: %s, save: false }",
		route.Index, route.Device, levelProps(volumes, mute))
}

// setRouteLevel writes a level on a speaker's Route. The write names
// the device object and not the node, and the channel count comes
// from the route's own levels.
func setRouteLevel(ctx context.Context, device int, route pwRoute, level levelWrite) error {
	var levels []float64
	if level.Volume != nil {
		levels = volumeLevels(*level.Volume, channelCount(route.Volumes))
	}
	return setParam(ctx, device, "Route", routeProps(route, levels, level.Mute))
}
