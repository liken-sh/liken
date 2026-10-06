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
// readings and its spec, or nil for a kind with no value to show.
// readings is nil before the driver reports. maximum answers the
// maximum that the driver defines for a number, for a value that INDI
// gives no unit.
func deviceDisplay(d *device, readings any, maximum func(property, member string) (float64, bool)) any {
	switch d.kind {
	case observatory.CameraKind:
		r, _ := readings.(observatory.CameraReadings)
		var out observatory.CameraDisplay
		if t := d.object.Spec.Temperature; t != nil {
			out.Setpoint = quantity(*t, 1, "°C")
		}
		if r.Temperature != nil {
			out.Temperature = quantity(*r.Temperature, 1, "°C")
		}
		if r.Cooler != nil {
			out.Cooler = onOff(*r.Cooler)
		}
		if r.CoolerPower != nil {
			out.CoolerPower = quantity(*r.CoolerPower, 0, "%")
		}
		out.Exposure = string(r.ExposureState)
		if r.ExposureState == indi.Busy && r.ExposureRemaining != nil {
			out.Exposure = quantity(*r.ExposureRemaining, 1, "s")
		}
		return out
	case observatory.MountKind:
		r, _ := readings.(observatory.MountReadings)
		var out observatory.MountDisplay
		if r.RightAscension != nil {
			out.RightAscension = rightAscension(*r.RightAscension)
		}
		if r.Declination != nil {
			out.Declination = declination(*r.Declination)
		}
		return out
	case observatory.DomeKind:
		r, _ := readings.(observatory.DomeReadings)
		return observatory.DomeDisplay{Azimuth: optionalText(r.Azimuth, func(v float64) string { return quantity(v, 1, "°") })}
	case observatory.FocuserKind:
		r, _ := readings.(observatory.FocuserReadings)
		var out observatory.FocuserDisplay
		if r.Position != nil {
			out.Position = quantity(float64(*r.Position), 0, "steps")
		}
		return out
	case observatory.RotatorKind:
		r, _ := readings.(observatory.RotatorReadings)
		return observatory.RotatorDisplay{Angle: optionalText(r.Angle, func(v float64) string { return quantity(v, 1, "°") })}
	case observatory.FlatPanelKind:
		r, _ := readings.(observatory.FlatPanelReadings)
		return observatory.FlatPanelDisplay{Brightness: optionalText(r.Brightness, func(v float64) string {
			if top, ok := maximum("FLAT_LIGHT_INTENSITY", "FLAT_LIGHT_INTENSITY_VALUE"); ok {
				return quantity(v, 0, "") + " of " + quantity(top, 0, "")
			}
			return quantity(v, 0, "")
		})}
	case observatory.SkyQualityMeterKind:
		r, _ := readings.(observatory.SkyQualityMeterReadings)
		return observatory.SkyQualityMeterDisplay{Brightness: optionalText(r.Brightness, func(v float64) string { return quantity(v, 2, "mag/arcsec²") })}
	case observatory.ReceiverKind:
		r, _ := readings.(observatory.ReceiverReadings)
		return observatory.ReceiverDisplay{Frequency: optionalText(r.Frequency, frequency)}
	}
	return nil
}

func optionalText(v *float64, format func(float64) string) string {
	if v == nil {
		return ""
	}
	return format(*v)
}
