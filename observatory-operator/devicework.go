package main

// What the steps send each kind of device, built against what the
// simulators of the indi-simulators image define and answer. A device
// whose driver lacks a property, such as a guide camera with no cooler,
// is noted in the step's message, and the step goes on.

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// connectOrder is the order in which Connect connects a telescope's
// devices. The cameras come last, because a camera snoops the mount,
// the focuser, the filter wheel, the rotator, and the sky quality meter
// of its train, and reads their values when it connects. Disconnect
// runs in the reverse order. The Switch devices connect earlier, in
// PowerOn, because they power the others.
var connectOrder = []observatory.Kind{
	observatory.MountKind, observatory.GPSKind, observatory.PolarAlignerKind,
	observatory.FocuserKind, observatory.FilterWheelKind, observatory.RotatorKind,
	observatory.DustCapKind, observatory.FlatPanelKind, observatory.SkyQualityMeterKind,
	observatory.ReceiverKind, observatory.CameraKind,
}

// siteOrder is the order in which StartSite connects an observatory's
// own devices, after its Switch devices.
var siteOrder = []observatory.Kind{
	observatory.WeatherStationKind, observatory.SkyQualityMeterKind,
	observatory.DomeKind, observatory.ReceiverKind,
}

// inOrder answers the devices in the order of kinds, and by name in
// each kind. A device of a kind that the order does not name is left
// out.
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

// The temperatures of a camera's cooler, in degrees Celsius.
const (
	// coolTolerance is how close to its setpoint a sensor must be for
	// Prepare to count it as cooled.
	coolTolerance = 0.5
	// warmTarget is the setpoint that Secure warms a cooled sensor to
	// before it switches the cooler off. A sensor that loses its cooler
	// at -10 °C warms by tens of degrees in seconds, and the stress of
	// that change can crack it. Warming under control first limits the
	// rate.
	warmTarget = 5.0
)

// coolers answers the cameras of a telescope that the step must cool or
// warm, and notes each camera whose driver has no cooler.
func coolable(ctx context.Context, cameras []handle, notes *[]string) []handle {
	var out []handle
	for _, h := range cameras {
		p, ok := h.property(ctx, "CCD_TEMPERATURE")
		switch {
		case !ok:
			*notes = append(*notes, fmt.Sprintf("no cooler on %s: no CCD_TEMPERATURE", h))
		case p.Perm == indi.ReadOnly:
			*notes = append(*notes, fmt.Sprintf("no cooler on %s: CCD_TEMPERATURE is read-only", h))
		default:
			out = append(out, h)
		}
	}
	return out
}

// setTemperature sends a cooler setpoint and waits until the sensor is
// within coolTolerance of it, or until the driver reports Alert, or
// until ctx ends. report says how far the sensor is.
func setTemperature(ctx context.Context, h handle, target float64, report func(string)) error {
	if _, err := h.client().SetNumbers(h.name, "CCD_TEMPERATURE", map[string]float64{"CCD_TEMPERATURE_VALUE": target}); err != nil {
		return fmt.Errorf("%s: %w", h, err)
	}
	last := math.NaN()
	err := h.client().WaitFor(ctx, func(s *indi.Store) bool {
		p, ok := s.Property(h.name, "CCD_TEMPERATURE")
		if !ok {
			return false
		}
		if p.State == indi.Alert {
			return true
		}
		now, _ := number(p, "CCD_TEMPERATURE_VALUE")
		last = now
		return math.Abs(now-target) <= coolTolerance
	})
	if err != nil {
		return fmt.Errorf("%s reached %s of %s: %w", h, quantity(last, 1, "°C"), quantity(target, 1, "°C"), err)
	}
	if p, _ := h.client().Property(h.name, "CCD_TEMPERATURE"); p.State == indi.Alert {
		return fmt.Errorf("%s: indi: %s.CCD_TEMPERATURE is Alert", h, h.name)
	}
	report(fmt.Sprintf("reached %s on %s", quantity(target, 1, "°C"), h))
	return nil
}

// location writes the observatory's location to a mount or a GPS.
// GEOGRAPHIC_COORD takes longitude from 0 to 360 degrees east.
func location(ctx context.Context, h handle, at observatory.Location) (bool, error) {
	longitude := at.Longitude
	if longitude < 0 {
		longitude += 360
	}
	return h.setNumbers(ctx, "GEOGRAPHIC_COORD", map[string]float64{"LAT": at.Latitude, "LONG": longitude, "ELEV": at.Elevation})
}

// activeDevices writes the members of ACTIVE_DEVICES that the property
// has, and leaves out the others. A member that names no device is
// empty, so a camera snoops no focuser of another train on the same
// server.
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

// filterNames writes a filter wheel's filters to FILTER_NAME, one name
// for each slot from slot 1, and notes the names that have no slot.
func filterNames(ctx context.Context, h handle, filters []string, notes *[]string) (bool, error) {
	p, ok := h.property(ctx, "FILTER_NAME")
	if !ok {
		*notes = append(*notes, fmt.Sprintf("no FILTER_NAME on %s", h))
		return false, nil
	}
	values := map[string]string{}
	for i, name := range filters {
		member := "FILTER_SLOT_NAME_" + strconv.Itoa(i+1)
		if _, has := p.Member(member); !has {
			*notes = append(*notes, fmt.Sprintf("no slot for %d of the filters on %s (%d slots)", len(filters)-i, h, len(p.Members)))
			break
		}
		values[member] = name
	}
	return h.setTexts(ctx, "FILTER_NAME", values)
}

// optional writes a setting that a person may leave out, to a property
// the driver may not define, and notes a property the driver lacks.
func optional(ctx context.Context, h handle, property string, values map[string]float64, notes *[]string) (bool, error) {
	if _, ok := h.property(ctx, property); !ok {
		*notes = append(*notes, fmt.Sprintf("no %s on %s", property, h))
		return false, nil
	}
	return h.setNumbers(ctx, property, values)
}

// on reports whether one member of a switch property is On now.
func on(h handle, property, member string) bool {
	p, ok := h.client().Property(h.name, property)
	if !ok {
		return false
	}
	m, has := p.Member(member)
	return has && m.Switch
}

// busy reports whether a property's state is Busy now.
func busy(h handle, property string) bool {
	p, ok := h.client().Property(h.name, property)
	return ok && p.State == indi.Busy
}

// outputs answers the Switch outputs that the devices name in
// spec.power, by switch, with each switch's outputs sorted.
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

// sortedKeys answers a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
