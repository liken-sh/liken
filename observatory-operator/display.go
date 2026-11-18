package main

// Formatting of the values that a person reads in `kubectl get`. A
// status stores its numbers without units, for programs, and the
// field's description gives the unit. A display block has the same
// values as text with the unit, such as -9.8 °C, and the printer
// columns show that text, because a column of bare numbers does not
// say what it measures.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// quantity formats v with at most decimals places and no trailing
// zeros, followed by the unit. SI writes a degree sign with no space
// after the number, and every other unit follows a space.
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

// frequency formats a frequency in hertz as megahertz, because radio
// astronomy gives its spectral lines in that unit, such as 1420.406 MHz.
func frequency(hertz float64) string { return quantity(hertz/1e6, 3, "MHz") }

// rightAscension formats hours from 0 to 24 as 19h17m21s, rounded to
// the second. Every field has two digits so that a column of values
// lines up.
func rightAscension(hours float64) string {
	seconds := int64(math.Round(hours*3600)) % (24 * 3600)
	return fmt.Sprintf("%02dh%02dm%02ds", seconds/3600, seconds/60%60, seconds%60)
}

// declination formats degrees from -90 to 90 as +12°34′56″, rounded to
// the arcsecond, and always writes the sign. The prime and double prime
// characters are one column wide in a terminal, like the degree sign,
// so a column of values lines up.
func declination(degrees float64) string {
	sign := "+"
	if degrees < 0 {
		sign = "-"
	}
	seconds := int64(math.Round(math.Abs(degrees) * 3600))
	return fmt.Sprintf("%s%02d°%02d′%02d″", sign, seconds/3600, seconds/60%60, seconds%60)
}

// duration formats a step's time rounded to the second, using at most
// the two largest units: 7 s, 2 min 2 s, 1 h 5 min.
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

// weatherUnits maps the unit that a weather driver puts in a
// parameter's label, such as "Wind (kph)", to the symbol to display.
var weatherUnits = map[string]string{
	"C": "°C", "F": "°F", "%": "%", "kph": "km/h", "mph": "mph",
	"m/s": "m/s", "mm": "mm", "hPa": "hPa", "mbar": "mbar",
}

// weatherText formats a weather parameter's value with the unit in the
// parentheses of its label. A label without parentheses has no unit, as
// with INDI's WEATHER_FORECAST, so only the number is written.
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

// deviceDisplay returns the display block for a device's kind, built
// from its typed fields, its limits, and its spec. It returns nil for a
// kind with nothing to show. standard is nil before the driver reports.
// A flat panel's brightness is shown as a fraction of the maximum in
// its limits, because INDI gives FLAT_LIGHT_INTENSITY no unit.
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

// cameraDisplay shows the setpoint of the cool action in the camera's
// activation, even before the driver reports. While an exposure runs
// and the driver reports the time left, Exposure is that time.
// Otherwise it is the state of CCD_EXPOSURE as one word.
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
