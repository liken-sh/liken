package main

// The readings of the kinds that have them, and the list of the
// properties that no typed field holds, from what the device's driver
// reports on its server. A reading is absent until the driver sends the
// properties it comes from.

import (
	"math"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reader reads the properties that one resource holds of its INDI
// device (devicesplit.go): the ones its typed fields hold, in the same
// window. A resource's readings and state conditions read these alone,
// so each of them follows the typed fields of the same status.
type reader struct {
	held  []indi.Property
	index map[string]int
}

// newReader reads a list of properties, in the driver's order.
func newReader(held []indi.Property) reader {
	index := make(map[string]int, len(held))
	for i, p := range held {
		index[p.Name] = i
	}
	return reader{held: held, index: index}
}

func (r reader) property(name string) (indi.Property, bool) {
	i, ok := r.index[name]
	if !ok {
		return indi.Property{}, false
	}
	return r.held[i], true
}

func (r reader) number(property, member string) *float64 {
	p, ok := r.property(property)
	if !ok {
		return nil
	}
	v, ok := number(p, member)
	if !ok {
		return nil
	}
	return &v
}

// readings answers the readings block of a device's kind, or nil for a
// kind with none. A reading holds what the operator derives from
// several values, which no typed field holds alone: what a mount does,
// and the name of the filter in a wheel's slot.
func readings(kind observatory.Kind, r reader) any {
	switch kind {
	case observatory.MountKind:
		return observatory.MountReadings{State: mountState(r.property)}
	case observatory.FilterWheelKind:
		return filterReadings(r)
	}
	return nil
}

// mountState names what a mount does, in this order:
//
//   - A Busy TELESCOPE_PARK is Parking or Unparking, toward the switch
//     that is On, because a park moves the mount whatever else it
//     reports.
//   - PARK On is Parked, unless the light is Alert. libindi's
//     telescope answers an abort during a park with both switches Off
//     and the light Alert, but a driver can leave PARK On after a
//     park fails, and then the switch does not say where the mount
//     is.
//   - A Busy EQUATORIAL_EOD_COORD is Slewing. A slew that ends in
//     tracking reads Slewing until the coordinates settle.
//   - TRACK_ON On is Tracking, and anything else is Stopped.
//
// Every mount driver defines EQUATORIAL_EOD_COORD, and a disconnected
// driver deletes it, so the state is absent until the driver defines
// it. A mount with no park or no tracking defines no TELESCOPE_PARK or
// TELESCOPE_TRACK_STATE, and the rules for the missing property do not
// match.
func mountState(property func(string) (indi.Property, bool)) observatory.MountState {
	park, _ := property("TELESCOPE_PARK")
	track, _ := property("TELESCOPE_TRACK_STATE")
	coordinates, ok := property("EQUATORIAL_EOD_COORD")
	if !ok {
		return ""
	}
	switch {
	case park.State == indi.Busy && isOn(park, "UNPARK"):
		return observatory.MountUnparking
	case park.State == indi.Busy && isOn(park, "PARK"):
		return observatory.MountParking
	case park.State != indi.Alert && isOn(park, "PARK"):
		return observatory.MountParked
	case coordinates.State == indi.Busy:
		return observatory.MountSlewing
	case isOn(track, "TRACK_ON"):
		return observatory.MountTracking
	}
	return observatory.MountStopped
}

// filterReadings names the filter in the light path: the member of
// FILTER_NAME for the slot that FILTER_SLOT gives, counted from 1.
func filterReadings(r reader) observatory.FilterWheelReadings {
	var out observatory.FilterWheelReadings
	v := r.number("FILTER_SLOT", "FILTER_SLOT_VALUE")
	if v == nil {
		return out
	}
	if p, ok := r.property("FILTER_NAME"); ok {
		if m, ok := p.Member("FILTER_SLOT_NAME_" + strconv.Itoa(int(math.Round(*v)))); ok {
			out.Filter = m.Text
		}
	}
	return out
}

// safety reads a weather light: Ok is Safe, Busy is Warning, Alert is
// Danger, and Idle is Unknown, as INDI's weather base class sets them.
func safety(light indi.State) observatory.Safety {
	switch light {
	case indi.Ok:
		return observatory.SafetySafe
	case indi.Busy:
		return observatory.SafetyWarning
	case indi.Alert:
		return observatory.SafetyDanger
	}
	return observatory.SafetyUnknown
}

// stationSafety reads a station's verdict from SAFETY_STATUS: the
// SAFETY light, or the property's state when the light is Idle. The
// weather simulator leaves the light Idle and sets the state alone, so
// a station that reads only the light would stay Unknown.
func stationSafety(p indi.Property) observatory.Safety {
	if m, ok := p.Member("SAFETY"); ok && m.Light != indi.Idle {
		return safety(m.Light)
	}
	return safety(p.State)
}

// typedSafety reads a station's verdict from its typed safety field,
// as stationSafety reads SAFETY_STATUS.
func typedSafety(s observatory.WeatherStationSafetyStatus) observatory.Safety {
	if s.Value != "" && s.Value != indi.Idle {
		return safety(s.Value)
	}
	return safety(s.State)
}

// properties lists properties as a status shows them, with each number
// in its member's format and no BLOB data.
func properties(list []indi.Property) []observatory.Property {
	var out []observatory.Property
	for _, p := range list {
		property := observatory.Property{
			Name: p.Name, Label: p.Label, Group: p.Group, Type: p.Type,
			Permission: p.Perm, State: p.State, Rule: p.Rule,
		}
		for _, m := range p.Members {
			member := observatory.Member{Name: m.Name, Label: m.Label}
			switch p.Type {
			case indi.NumberType:
				member.Value = strings.TrimSpace(indi.FormatNumber(m.Format, m.Number))
				member.Min, member.Max = &m.Min, &m.Max
				if m.Step != 0 {
					member.Step = &m.Step
				}
			case indi.SwitchType:
				member.Value = "Off"
				if m.Switch {
					member.Value = "On"
				}
			case indi.TextType:
				member.Value = m.Text
			case indi.LightType:
				member.Value = string(m.Light)
			}
			property.Members = append(property.Members, member)
		}
		out = append(out, property)
	}
	return out
}
