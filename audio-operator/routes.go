package main

// A Bluetooth speaker is two objects in PipeWire's graph: a device,
// which WirePlumber's bluez monitor builds from the BlueZ transport,
// and a node, which plays into the device. The device has a Route for
// each direction of its profile, and the props of a Route have the
// level.
//
// The operator writes a speaker's level on the device's Route, because
// the bluez5 plugin passes a Route level to
// spa_bt_transport_set_volume, which writes the Volume property of
// org.bluez.MediaTransport1. That is AVRCP absolute volume, so the
// speaker's own volume changes. A level written on the node changes
// only a software gain, because the A2DP follower accepts no volume
// key (spa/plugins/bluez5/media-sink.c in pipewire 1.4.2).
//
// The operator writes the Route for a Sink's spec.volume.level and for
// a volume request in status.session.volumeAsk (asks.go). A Route with no
// volumeStep means that the transport has no volume of its own, and the
// operator writes the node's software gain instead, as for an ALSA
// endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
)

// The Output route of a device is the speaker. The operator plays to a
// speaker and does not record from one, so it reads no other route.
const outputRouteDirection = "Output"

// pwRoute is one Route of a device. A write names the route by Index
// and Device, and the rest is the route's props.
type pwRoute struct {
	Index   int
	Device  int
	Mute    bool
	Volumes []float64
	// AbsoluteVolume is true when the route has a read-only volumeStep,
	// which PipeWire sets only for a transport with a volume of its own.
	// The step is 1/(max+1) of the peer's volume scale.
	AbsoluteVolume bool
}

// routeParam is a Route as pw-dump prints it, with only the fields that
// a level write and the status read.
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

// outputRoute returns the Output route from a device's Route params. It
// returns nil for a device with no Output route, so the operator
// reports no level and does not write to route index 0.
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

// routeProps builds the POD for a Route write. save is false so that
// WirePlumber does not store the level, because the operator applies
// its declared level again after a restart.
func routeProps(route pwRoute, volumes []float64, mute *bool) string {
	return fmt.Sprintf("{ index: %d, device: %d, props: %s, save: false }",
		route.Index, route.Device, levelProps(volumes, mute))
}

// setRouteLevel writes a level on a speaker's Route. The write names
// the device object, not the node, and reads the channel count from
// the route's current levels.
func setRouteLevel(ctx context.Context, device int, route pwRoute, level levelWrite) error {
	var levels []float64
	if level.Volume != nil {
		levels = volumeLevels(*level.Volume, channelCount(route.Volumes))
	}
	return setParam(ctx, device, "Route", routeProps(route, levels, level.Mute))
}
