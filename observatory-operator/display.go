package main

// The values that a person reads in `kubectl get`, with their units.
// Each status keeps its numbers plain for programs, with the unit in
// the field's description, and a display block beside them holds the
// same values as text with the unit, such as -9.8 °C. A printer column
// shows the text, because a column of bare numbers does not say what
// they measure.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// quantity writes a number with at most decimals places and no
// trailing zeros, then its unit. A degree sign follows the number with
// no space, as SI writes a plane angle, and every other unit follows a
// space.
func quantity(v float64, decimals int, unit string) string {
	text := strconv.FormatFloat(v, 'f', decimals, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		text = "0"
	}
	switch unit {
	case "":
		return text
	case "°":
		return text + unit
	}
	return text + " " + unit
}

func latitude(v float64) string {
	if v < 0 {
		return quantity(-v, 4, "°") + " S"
	}
	return quantity(v, 4, "°") + " N"
}

func longitude(v float64) string {
	if v < 0 {
		return quantity(-v, 4, "°") + " W"
	}
	return quantity(v, 4, "°") + " E"
}

// frequency writes a frequency in hertz as megahertz, the unit that
// radio astronomy names its lines in, such as 1420.406 MHz.
func frequency(hertz float64) string { return quantity(hertz/1e6, 3, "MHz") }

// rightAscension writes hours from 0 to 24 as 19h17m21s, rounded to
// the second. Every field has two digits, so a column of them lines
// up.
func rightAscension(hours float64) string {
	seconds := int64(math.Round(hours*3600)) % (24 * 3600)
	return fmt.Sprintf("%02dh%02dm%02ds", seconds/3600, seconds/60%60, seconds%60)
}

// declination writes degrees from -90 to 90 as +12°34′56″, rounded to
// the arcsecond, with the sign always written. The prime and the
// double prime are one column wide in a terminal, as the degree sign
// is, so a column of them lines up.
func declination(degrees float64) string {
	sign := "+"
	if degrees < 0 {
		sign = "-"
	}
	seconds := int64(math.Round(math.Abs(degrees) * 3600))
	return fmt.Sprintf("%s%02d°%02d′%02d″", sign, seconds/3600, seconds/60%60, seconds%60)
}

// duration writes a step's time to the second, in the largest two
// units: 7 s, 2 min 2 s, 1 h 5 min.
func duration(d time.Duration) string {
	s := int64(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600 && s%60 == 0:
		return fmt.Sprintf("%d min", s/60)
	case s < 3600:
		return fmt.Sprintf("%d min %d s", s/60, s%60)
	case s%3600/60 == 0:
		return fmt.Sprintf("%d h", s/3600)
	}
	return fmt.Sprintf("%d h %d min", s/3600, s%3600/60)
}

// weatherUnits maps the unit that a weather driver writes in a
// parameter's label, such as "Wind (kph)", to its SI symbol.
var weatherUnits = map[string]string{
	"C": "°C", "F": "°F", "%": "%", "kph": "km/h", "mph": "mph",
	"m/s": "m/s", "mm": "mm", "hPa": "hPa", "mbar": "mbar",
}

// weatherText writes a weather parameter's value with the unit that
// its label names in parentheses. INDI gives WEATHER_FORECAST, and any
// label with no parentheses, no unit, so its number stands alone.
func weatherText(label string, v float64) string {
	open, end := strings.LastIndex(label, "("), strings.LastIndex(label, ")")
	if open < 0 || end < open {
		return quantity(v, 1, "")
	}
	unit := label[open+1 : end]
	if symbol, ok := weatherUnits[unit]; ok {
		unit = symbol
	}
	return quantity(v, 1, unit)
}

func onOff(v bool) string {
	if v {
		return "On"
	}
	return "Off"
}

// deviceDisplay answers the display block of a device's kind, from its
// typed fields, its limits, and its spec, or nil for a kind with no
// value to show. standard is nil before the driver reports. A flat
// panel's brightness shows against the maximum in its limits, because
// INDI gives FLAT_LIGHT_INTENSITY no unit.
func deviceDisplay(d *device, standard any, limits map[string]map[string]observatory.Limit) any {
	degrees := func(v float64) string { return quantity(v, 1, "°") }
	switch d.kind {
	case observatory.CameraKind:
		s, _ := standard.(*observatory.CameraStandard)
		return cameraDisplay(d, s)
	case observatory.MountKind:
		var out observatory.MountDisplay
		if s, _ := standard.(*observatory.MountStandard); s != nil && s.Equatorial != nil {
			out.RightAscension = optionalText(s.Equatorial.RightAscension, rightAscension)
			out.Declination = optionalText(s.Equatorial.Declination, declination)
		}
		return out
	case observatory.DomeKind:
		var out observatory.DomeDisplay
		if s, _ := standard.(*observatory.DomeStandard); s != nil && s.Azimuth != nil {
			out.Azimuth = optionalText(s.Azimuth.Value, degrees)
		}
		return out
	case observatory.FocuserKind:
		var out observatory.FocuserDisplay
		if s, _ := standard.(*observatory.FocuserStandard); s != nil && s.Position != nil {
			out.Position = optionalText(s.Position.Value, func(v float64) string { return quantity(math.Round(v), 0, "steps") })
		}
		return out
	case observatory.RotatorKind:
		var out observatory.RotatorDisplay
		if s, _ := standard.(*observatory.RotatorStandard); s != nil && s.Angle != nil {
			out.Angle = optionalText(s.Angle.Value, degrees)
		}
		return out
	case observatory.FlatPanelKind:
		var out observatory.FlatPanelDisplay
		if s, _ := standard.(*observatory.FlatPanelStandard); s != nil && s.Brightness != nil {
			out.Brightness = optionalText(s.Brightness.Value, func(v float64) string {
				if top, ok := limits["FLAT_LIGHT_INTENSITY"]["FLAT_LIGHT_INTENSITY_VALUE"]; ok {
					return quantity(v, 0, "") + " of " + quantity(top.Max, 0, "")
				}
				return quantity(v, 0, "")
			})
		}
		return out
	case observatory.ReceiverKind:
		var out observatory.ReceiverDisplay
		if s, _ := standard.(*observatory.ReceiverStandard); s != nil && s.ReceiverSettings != nil {
			out.Frequency = optionalText(s.ReceiverSettings.Frequency, frequency)
		}
		return out
	case observatory.SwitchKind:
		var on []string
		if s, _ := standard.(*observatory.SwitchStandard); s != nil {
			for _, output := range s.Outputs {
				if output.Value == "ON" {
					on = append(on, strconv.Itoa(int(output.Number)))
				}
			}
		}
		return observatory.SwitchDisplay{On: strings.Join(on, ", ")}
	}
	return nil
}

// cameraDisplay shows a camera's temperature, cooler, and exposure,
// and the setpoint of its activation's cool action. While an exposure
// runs, the exposure shows the time left, and otherwise the state of
// CCD_EXPOSURE in one word.
func cameraDisplay(d *device, s *observatory.CameraStandard) observatory.CameraDisplay {
	var out observatory.CameraDisplay
	if t := coolSetpoint(d); t != nil {
		out.Setpoint = quantity(*t, 1, "°C")
	}
	if s == nil {
		return out
	}
	if s.Temperature != nil {
		out.Temperature = optionalText(s.Temperature.Value, func(v float64) string { return quantity(v, 1, "°C") })
	}
	if s.Cooler != nil && s.Cooler.Value != "" {
		out.Cooler = onOff(s.Cooler.Value == "COOLER_ON")
	}
	if s.CoolerPower != nil {
		out.CoolerPower = optionalText(s.CoolerPower.Value, func(v float64) string { return quantity(v, 0, "%") })
	}
	if s.Exposure != nil {
		out.Exposure = string(exposureState(s.Exposure.State))
		if s.Exposure.State == indi.Busy && s.Exposure.Value != nil {
			out.Exposure = quantity(*s.Exposure.Value, 1, "s")
		}
	}
	return out
}

// exposureState names the state of CCD_EXPOSURE.
func exposureState(state indi.State) observatory.ExposureState {
	switch state {
	case indi.Busy:
		return observatory.ExposureExposing
	case indi.Ok:
		return observatory.ExposureDone
	case indi.Alert:
		return observatory.ExposureFailed
	}
	return observatory.ExposureIdle
}

func optionalText(v *float64, format func(float64) string) string {
	if v == nil {
		return ""
	}
	return format(*v)
}
