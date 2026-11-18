package main

// The property names here are the ones that the drivers in the
// indi-simulators image define. When a real driver lacks a property,
// such as a guide camera with no cooler, the step adds a note to its
// message and continues.

import (
	"context"
	"fmt"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// connectOrder is the order in which a telescope's devices connect, by
// kind. Cameras come last because a camera snoops the mount, focuser,
// filter wheel, and rotator of its train and reads their values when it
// connects. Switch devices are not listed because they power the other
// devices, so PowerOn connects them first.
var connectOrder = []observatory.Kind{
	observatory.MountKind, observatory.GPSKind, observatory.PolarAlignerKind,
	observatory.FocuserKind, observatory.FilterWheelKind, observatory.RotatorKind,
	observatory.DustCapKind, observatory.FlatPanelKind,
	observatory.ReceiverKind, observatory.CameraKind,
}

// siteOrder is the order in which an observatory's own devices connect,
// after its Switch devices.
var siteOrder = []observatory.Kind{
	observatory.WeatherStationKind, observatory.DomeKind, observatory.ReceiverKind,
}

// inOrder sorts devices by the position of their kind in kinds, and
// leaves out a device whose kind is not in kinds.
func inOrder(devices []*device, kinds []observatory.Kind) []*device {
	var out []*device
	for _, kind := range kinds {
		for _, d := range devices {
			if d.kind == kind {
				out = append(out, d)
			}
		}
	}
	return out
}

// GEOGRAPHIC_COORD takes a longitude from 0 to 360 degrees east.
func location(ctx context.Context, h handle, at observatory.Location) (bool, error) {
	longitude := at.Longitude
	if longitude < 0 {
		longitude += 360
	}
	return h.setNumbers(ctx, "GEOGRAPHIC_COORD", map[string]float64{"LAT": at.Latitude, "LONG": longitude, "ELEV": at.Elevation})
}

// activeDevices writes only the members of want that the driver's
// ACTIVE_DEVICES has. It writes an empty value too, so that a camera
// stops snooping a device of another train on the same server.
func activeDevices(ctx context.Context, h handle, want map[string]string) (bool, error) {
	p, ok := h.property(ctx, "ACTIVE_DEVICES")
	if !ok {
		return false, nil
	}
	values := map[string]string{}
	for member, device := range want {
		if _, has := p.Member(member); has {
			values[member] = device
		}
	}
	if len(values) == 0 {
		return false, nil
	}
	return h.setTexts(ctx, "ACTIVE_DEVICES", values)
}

// optional writes a property that the driver may not define. When the
// driver lacks it, optional appends a note and writes nothing.
func optional(ctx context.Context, h handle, property string, values map[string]float64, notes *[]string) (bool, error) {
	if _, ok := h.property(ctx, property); !ok {
		*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
		return false, nil
	}
	return h.setNumbers(ctx, property, values)
}

// on reports whether a member of a switch property is On in the
// client's store.
func on(h handle, property, member string) bool {
	p, ok := h.client().Property(h.name, property)
	if !ok {
		return false
	}
	m, has := p.Member(member)
	return has && m.Switch
}

func busy(h handle, property string) bool {
	p, ok := h.client().Property(h.name, property)
	return ok && p.State == indi.Busy
}

// outputs returns the outputs that devices name in spec.power, sorted
// and without duplicates, for each Switch.
func outputs(devices []*device) map[string][]int32 {
	out := map[string][]int32{}
	for _, d := range devices {
		if p := d.object.Spec.Power; p != nil && !slices.Contains(out[p.Switch], p.Output) {
			out[p.Switch] = append(out[p.Switch], p.Output)
		}
	}
	for name := range out {
		slices.Sort(out[name])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
