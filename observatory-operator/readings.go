package main

// The typed readings of each device kind, and the list of every
// property, from what the device's driver reports on its server. A
// reading is absent until the driver sends the property it comes from.

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// reader reads one device's properties from its client's store.
type reader struct {
	c    *indi.Client
	name string
}

func (r reader) property(name string) (indi.Property, bool) { return r.c.Property(r.name, name) }

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

// maximum answers the largest value that the driver defines for a
// number.
func (r reader) maximum(property, member string) (float64, bool) {
	p, ok := r.property(property)
	if !ok {
		return 0, false
	}
	m, ok := p.Member(member)
	return m.Max, ok
}

func (r reader) on(property, member string) *bool {
	p, ok := r.property(property)
	if !ok {
		return nil
	}
	m, ok := p.Member(member)
	if !ok {
		return nil
	}
	return &m.Switch
}

func (r reader) state(property string) indi.State {
	p, _ := r.property(property)
	return p.State
}

// readings answers the readings block of a device's kind.
func readings(kind observatory.Kind, r reader) any {
	switch kind {
	case observatory.MountKind:
		return observatory.MountReadings{
			RightAscension: r.number("EQUATORIAL_EOD_COORD", "RA"),
			Declination:    r.number("EQUATORIAL_EOD_COORD", "DEC"),
			State:          mountState(r.property),
		}
	case observatory.GPSKind:
		return gpsReadings(r)
	case observatory.PolarAlignerKind:
		return observatory.PolarAlignerReadings{Adjustment: r.state("PAC_MANUAL_ADJUSTMENT")}
	case observatory.CameraKind:
		out := observatory.CameraReadings{
			Temperature: r.number("CCD_TEMPERATURE", "CCD_TEMPERATURE_VALUE"),
			Cooler:      r.on("CCD_COOLER", "COOLER_ON"),
			CoolerPower: r.number("CCD_COOLER_POWER", "CCD_COOLER_VALUE"),
			Exposure:    exposure(r),
		}
		if out.Exposure == observatory.ExposureExposing {
			out.ExposureRemaining = r.number("CCD_EXPOSURE", "CCD_EXPOSURE_VALUE")
		}
		return out
	case observatory.FilterWheelKind:
		return filterReadings(r)
	case observatory.FocuserKind:
		var out observatory.FocuserReadings
		if v := r.number("ABS_FOCUS_POSITION", "FOCUS_ABSOLUTE_POSITION"); v != nil {
			position := int64(math.Round(*v))
			out.Position = &position
		}
		return out
	case observatory.RotatorKind:
		return observatory.RotatorReadings{Angle: r.number("ABS_ROTATOR_ANGLE", "ANGLE")}
	case observatory.DustCapKind:
		return observatory.DustCapReadings{Cover: cover(r, "CAP_PARK", "UNPARK", "PARK")}
	case observatory.FlatPanelKind:
		return observatory.FlatPanelReadings{
			Light:      light(r),
			Brightness: r.number("FLAT_LIGHT_INTENSITY", "FLAT_LIGHT_INTENSITY_VALUE"),
		}
	case observatory.DomeKind:
		return observatory.DomeReadings{
			Azimuth: r.number("ABS_DOME_POSITION", "DOME_ABSOLUTE_POSITION"),
			Shutter: cover(r, "DOME_SHUTTER", "SHUTTER_OPEN", "SHUTTER_CLOSE"),
			Park:    domePark(r),
		}
	case observatory.WeatherStationKind:
		return weatherReadings(r)
	case observatory.SkyQualityMeterKind:
		return observatory.SkyQualityMeterReadings{Brightness: r.number("SKY_QUALITY", "SKY_BRIGHTNESS")}
	case observatory.SwitchKind:
		return switchReadings(r)
	case observatory.ReceiverKind:
		return observatory.ReceiverReadings{Frequency: r.number("RECEIVER_SETTINGS", "RECEIVER_FREQUENCY")}
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

// domePark reads DOME_PARK as a cover reads its switch: Moving while
// Busy, then the member that is On.
func domePark(r reader) observatory.DomePark {
	p, ok := r.property("DOME_PARK")
	switch {
	case !ok:
		return ""
	case p.State == indi.Busy:
		return observatory.DomeMoving
	case isOn(p, "PARK"):
		return observatory.DomeParked
	case isOn(p, "UNPARK"):
		return observatory.DomeUnparked
	}
	return ""
}

// exposure names the light of CCD_EXPOSURE.
func exposure(r reader) observatory.ExposureState {
	p, ok := r.property("CCD_EXPOSURE")
	if !ok {
		return ""
	}
	switch p.State {
	case indi.Busy:
		return observatory.ExposureExposing
	case indi.Ok:
		return observatory.ExposureDone
	case indi.Alert:
		return observatory.ExposureFailed
	}
	return observatory.ExposureIdle
}

// light names a flat panel's light from the switch of
// FLAT_LIGHT_CONTROL, as the Lit condition reads it.
func light(r reader) observatory.LightState {
	p, ok := r.property("FLAT_LIGHT_CONTROL")
	switch {
	case !ok:
		return ""
	case isOn(p, "FLAT_LIGHT_ON"):
		return observatory.StateLit
	case isOn(p, "FLAT_LIGHT_OFF"):
		return observatory.StateDark
	}
	return ""
}

// cover reads a cover's position from a switch property: Moving while
// Busy, Open when the open member is On, Closed when the closed member
// is On.
func cover(r reader, property, open, closed string) string {
	p, ok := r.property(property)
	if !ok {
		return ""
	}
	if p.State == indi.Busy {
		return observatory.CoverMoving
	}
	if m, _ := p.Member(open); m.Switch {
		return observatory.CoverOpen
	}
	if m, _ := p.Member(closed); m.Switch {
		return observatory.CoverClosed
	}
	return ""
}

func gpsReadings(r reader) observatory.GPSReadings {
	var out observatory.GPSReadings
	if p, ok := r.property("GEOGRAPHIC_COORD"); ok {
		fix := p.State == indi.Ok
		out.Fix = &fix
		out.Latitude = r.number("GEOGRAPHIC_COORD", "LAT")
		if v := r.number("GEOGRAPHIC_COORD", "LONG"); v != nil {
			longitude := *v
			if longitude > 180 {
				longitude -= 360
			}
			out.Longitude = &longitude
		}
		out.Elevation = r.number("GEOGRAPHIC_COORD", "ELEV")
	}
	if p, ok := r.property("TIME_UTC"); ok {
		if m, ok := p.Member("UTC"); ok {
			if at, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(m.Text)); err == nil {
				out.Time = &at
			}
		}
	}
	return out
}

func filterReadings(r reader) observatory.FilterWheelReadings {
	var out observatory.FilterWheelReadings
	v := r.number("FILTER_SLOT", "FILTER_SLOT_VALUE")
	if v == nil {
		return out
	}
	slot := int32(math.Round(*v))
	out.Slot = &slot
	if p, ok := r.property("FILTER_NAME"); ok {
		if m, ok := p.Member("FILTER_SLOT_NAME_" + strconv.Itoa(int(slot))); ok {
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

func weatherReadings(r reader) observatory.WeatherStationReadings {
	var out observatory.WeatherStationReadings
	if p, ok := r.property("SAFETY_STATUS"); ok {
		out.Safety = stationSafety(p)
	}
	status, _ := r.property("WEATHER_STATUS")
	if p, ok := r.property("WEATHER_PARAMETERS"); ok {
		for _, m := range p.Members {
			value := m.Number
			parameter := observatory.WeatherParameter{Name: m.Name, Label: m.Label, Value: &value, Text: weatherText(m.Label, value)}
			if light, ok := status.Member(m.Name); ok {
				parameter.Safety = safety(light.Light)
			}
			out.Parameters = append(out.Parameters, parameter)
		}
	}
	return out
}

func switchReadings(r reader) observatory.SwitchReadings {
	var out observatory.SwitchReadings
	out.Outputs = channels(r, "DIGITAL_OUTPUT_", "DIGITAL_OUTPUT_LABELS")
	out.Inputs = channels(r, "DIGITAL_INPUT_", "DIGITAL_INPUT_LABELS")
	for _, c := range out.Outputs {
		if c.On != nil && *c.On {
			out.On = append(out.On, c.Number)
		}
	}
	return out
}

// channels reads DIGITAL_OUTPUT_1, DIGITAL_OUTPUT_2, and on, until the
// first number the driver does not define.
func channels(r reader, prefix, labels string) []observatory.SwitchChannel {
	names, _ := r.property(labels)
	var out []observatory.SwitchChannel
	for n := 1; ; n++ {
		property := prefix + strconv.Itoa(n)
		if _, ok := r.property(property); !ok {
			return out
		}
		channel := observatory.SwitchChannel{Number: int32(n), On: r.on(property, "ON")}
		if m, ok := names.Member(property); ok {
			channel.Label = m.Text
		}
		out = append(out, channel)
	}
}

// properties lists every property of a device as its status shows it,
// with each number in its member's format and no BLOB data.
func properties(r reader) []observatory.Property {
	var out []observatory.Property
	for _, p := range r.c.Properties(r.name) {
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
