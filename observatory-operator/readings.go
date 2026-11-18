package main

// The readings of a device's status, and the list of its INDI
// properties that no typed status field holds. A reading is absent
// until the driver sends the properties it is derived from.

import (
	"math"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reader looks up the INDI properties that devicesplit.go assigns to
// one resource. The resource's typed status fields come from the same
// list, so its readings and state conditions agree with those fields.
type reader struct {
	held  []indi.Property
	index map[string]int
}

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

// readings returns the readings of a device kind, or nil for a kind
// with none. A reading is a value that the operator derives from
// several properties, such as what a mount is doing or the name of the
// filter in a wheel's current slot.
func readings(kind observatory.Kind, r reader) any {
	switch kind {
	case observatory.MountKind:
		return observatory.MountReadings{State: mountState(r.property)}
	case observatory.FilterWheelKind:
		return filterReadings(r)
	}
	return nil
}

// mountState works out what a mount is doing from its INDI properties.
// It checks these rules in order and returns the first match:
//
//   - TELESCOPE_PARK in state Busy means Parking or Unparking,
//     depending on which switch is On. A park moves the mount whatever
//     else the driver reports.
//   - PARK On means Parked, unless the TELESCOPE_PARK state is Alert.
//     libindi's telescope base class sets both switches Off and the
//     state Alert when a park is aborted, but a driver can leave PARK
//     On after a park fails, and then the switch does not show where
//     the mount is.
//   - EQUATORIAL_EOD_COORD in state Busy means Slewing. A slew that
//     ends in tracking reads Slewing until the coordinates settle.
//   - TRACK_ON On means Tracking. Anything else is Stopped.
//
// Every mount driver defines EQUATORIAL_EOD_COORD, and a driver deletes
// it when it disconnects, so mountState returns "" until the driver
// defines it. A mount with no park or no tracking does not define
// TELESCOPE_PARK or TELESCOPE_TRACK_STATE, and the rules for the
// missing property never match.
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

// filterReadings returns the name of the filter in the light path. It
// is the text of the FILTER_NAME member for the slot number in
// FILTER_SLOT, where slots count from 1.
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

// safety converts a weather light to a Safety. INDI's weather base
// class sets Ok for safe, Busy for a warning, Alert for danger, and
// Idle when it has no reading.
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

// stationSafety returns a station's Safety from the SAFETY light of
// SAFETY_STATUS, or from the property's state when the light is Idle.
// The weather simulator leaves the light Idle and sets only the state.
func stationSafety(p indi.Property) observatory.Safety {
	if m, ok := p.Member("SAFETY"); ok && m.Light != indi.Idle {
		return safety(m.Light)
	}
	return safety(p.State)
}

// typedSafety applies the rule of stationSafety to the typed safety
// status field.
func typedSafety(s observatory.WeatherStationSafetyStatus) observatory.Safety {
	if s.Value != "" && s.Value != indi.Idle {
		return safety(s.Value)
	}
	return safety(s.State)
}

// properties converts INDI properties to the form that status stores.
// It formats each number with its member's format and leaves out BLOB
// data.
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
