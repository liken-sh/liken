package main

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestTheUnitsOfEachValue(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"a sensor's temperature", quantity(-9.84, 1, "°C"), "-9.8 °C"},
		{"a whole setpoint", quantity(-10, 1, "°C"), "-10 °C"},
		{"a temperature that rounds to zero", quantity(-0.04, 1, "°C"), "0 °C"},
		{"a cooler's power", quantity(41.6, 0, "%"), "42 %"},
		{"an aperture", quantity(80, 1, "mm"), "80 mm"},
		{"a focuser's position", quantity(52000, 0, "steps"), "52000 steps"},
		{"an angle", quantity(90, 1, "°"), "90°"},
		{"an azimuth", quantity(123.44, 1, "°"), "123.4°"},
		{"an exposure", quantity(30, 1, "s"), "30 s"},
		{"a sky's brightness", quantity(21.3, 2, "mag/arcsec²"), "21.3 mag/arcsec²"},
		{"a latitude north", latitude(51.47692), "51.4769° N"},
		{"a latitude south", latitude(-33.8568), "33.8568° S"},
		{"a longitude west", longitude(-0.0005), "0.0005° W"},
		{"a longitude east", longitude(151.2093), "151.2093° E"},
		{"a frequency", frequency(1420405752), "1420.406 MHz"},
		{"a weather temperature", weatherText("Temperature (C)", 12.5), "12.5 °C"},
		{"a humidity", weatherText("Humidity (%)", 63), "63 %"},
		{"a wind speed", weatherText("Wind (kph)", 20), "20 km/h"},
		{"a rainfall", weatherText("Precip (mm)", 0), "0 mm"},
		{"a forecast, which has no unit", weatherText("Weather", 1), "1"},
		{"a few seconds", duration(7400 * time.Millisecond), "7 s"},
		{"a minute and more", duration(2*time.Minute + 2*time.Second), "2 min 2 s"},
		{"whole minutes", duration(20 * time.Minute), "20 min"},
		{"an hour and more", duration(time.Hour + 5*time.Minute + 9*time.Second), "1 h 5 min"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestRightAscensionAndDeclination(t *testing.T) {
	cases := []struct {
		name    string
		ra, dec float64
		wantRA  string
		wantDec string
	}{
		{"a star in the north", 19.28915271950613, 12.582222, "19h17m21s", "+12°34′56″"},
		{"a star in the south", 5.5, -5.25, "05h30m00s", "-05°15′00″"},
		{"seconds that round up to the next hour", 23.99999, 89.99999, "00h00m00s", "+90°00′00″"},
		{"the celestial equator", 0, 0, "00h00m00s", "+00°00′00″"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rightAscension(c.ra); got != c.wantRA {
				t.Errorf("right ascension %v = %q, want %q", c.ra, got, c.wantRA)
			}
			if got := declination(c.dec); got != c.wantDec {
				t.Errorf("declination %v = %q, want %q", c.dec, got, c.wantDec)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestTheDisplayOfACamera(t *testing.T) {
	camera := &device{kind: observatory.CameraKind}
	camera.object.Spec.Activation = []action{{Cool: &observatory.Temperature{Celsius: -10}}}
	cooler := func(on string) *observatory.CameraCoolerStatus { return &observatory.CameraCoolerStatus{Value: on} }
	cases := []struct {
		name     string
		standard any
		want     observatory.CameraDisplay
	}{
		{"a camera with no pod shows its setpoint", nil, observatory.CameraDisplay{Setpoint: "-10 °C"}},
		{
			"a cooling camera",
			&observatory.CameraStandard{Temperature: &observatory.CameraTemperatureStatus{Value: ptr(-9.84)}, Cooler: cooler("COOLER_ON"),
				CoolerPower: &observatory.CameraCoolerPowerStatus{Value: ptr(42.0)}, Exposure: &observatory.CameraExposureStatus{State: indi.Idle}},
			observatory.CameraDisplay{Temperature: "-9.8 °C", Setpoint: "-10 °C", Cooler: "On", CoolerPower: "42 %", Exposure: "Idle"},
		},
		{
			"a camera that exposes",
			&observatory.CameraStandard{Cooler: cooler("COOLER_OFF"), Exposure: &observatory.CameraExposureStatus{State: indi.Busy, Value: ptr(12.0)}},
			observatory.CameraDisplay{Setpoint: "-10 °C", Cooler: "Off", Exposure: "12 s"},
		},
		{
			"a camera that exposes and reports no time left",
			&observatory.CameraStandard{Exposure: &observatory.CameraExposureStatus{State: indi.Busy}},
			observatory.CameraDisplay{Setpoint: "-10 °C", Exposure: "Exposing"},
		},
		{
			"a camera whose exposure ended",
			&observatory.CameraStandard{Cooler: cooler("COOLER_OFF"), Exposure: &observatory.CameraExposureStatus{State: indi.Ok}},
			observatory.CameraDisplay{Setpoint: "-10 °C", Cooler: "Off", Exposure: "Done"},
		},
		{
			"a camera whose exposure failed",
			&observatory.CameraStandard{Exposure: &observatory.CameraExposureStatus{State: indi.Alert}},
			observatory.CameraDisplay{Setpoint: "-10 °C", Exposure: "Failed"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceDisplay(camera, c.standard, nil); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestTheDisplayOfEachKind(t *testing.T) {
	limits := map[string]map[string]observatory.Limit{"FLAT_LIGHT_INTENSITY": {"FLAT_LIGHT_INTENSITY_VALUE": {Max: 255}}}
	cases := []struct {
		name     string
		kind     observatory.Kind
		standard any
		limits   map[string]map[string]observatory.Limit
		want     any
	}{
		{"a mount", observatory.MountKind, &observatory.MountStandard{Equatorial: &observatory.MountEquatorialStatus{RightAscension: ptr(19.28915271950613), Declination: ptr(12.582222)}}, nil,
			observatory.MountDisplay{RightAscension: "19h17m21s", Declination: "+12°34′56″"}},
		{"a dome", observatory.DomeKind, &observatory.DomeStandard{Azimuth: &observatory.DomeAzimuthStatus{Value: ptr(123.44)}}, nil, observatory.DomeDisplay{Azimuth: "123.4°"}},
		{"a focuser", observatory.FocuserKind, &observatory.FocuserStandard{Position: &observatory.FocuserPositionStatus{Value: ptr(52000.0)}}, nil, observatory.FocuserDisplay{Position: "52000 steps"}},
		{"a rotator", observatory.RotatorKind, &observatory.RotatorStandard{Angle: &observatory.RotatorAngleStatus{Value: ptr(90.0)}}, nil, observatory.RotatorDisplay{Angle: "90°"}},
		{"a flat panel", observatory.FlatPanelKind, &observatory.FlatPanelStandard{Brightness: &observatory.FlatPanelBrightnessStatus{Value: ptr(128.0)}}, limits, observatory.FlatPanelDisplay{Brightness: "128 of 255"}},
		{"a flat panel with no range", observatory.FlatPanelKind, &observatory.FlatPanelStandard{Brightness: &observatory.FlatPanelBrightnessStatus{Value: ptr(128.0)}}, nil, observatory.FlatPanelDisplay{Brightness: "128"}},
		{"a receiver", observatory.ReceiverKind, &observatory.ReceiverStandard{ReceiverSettings: &observatory.ReceiverReceiverSettingsStatus{Frequency: ptr(1420405752.0)}}, nil, observatory.ReceiverDisplay{Frequency: "1420.406 MHz"}},
		{"a switch", observatory.SwitchKind, &observatory.SwitchStandard{Outputs: []observatory.SwitchOutputsStatus{{Number: 1, Value: "ON"}, {Number: 2, Value: "OFF"}, {Number: 3, Value: "ON"}}}, nil,
			observatory.SwitchDisplay{On: "1, 3"}},
		{"a mount with no typed fields", observatory.MountKind, nil, nil, observatory.MountDisplay{}},
		{"a kind with nothing to show", observatory.GPSKind, &observatory.GPSStandard{}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceDisplay(&device{kind: c.kind}, c.standard, c.limits); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// The printer columns read the display blocks, so each block holds the
// value with its unit while a reservation holds the east telescope.
func TestTheStatusShowsEachValueWithItsUnit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if d := camera.Status.Display; d.Temperature != "-10 °C" || d.Setpoint != "-10 °C" || (d.Cooler != "On" && d.Cooler != "Off") {
			t.Errorf("camera display = %+v", d)
		}
		mount, _ := decode[observatory.Mount](t, w.api, kindCollection(observatory.MountKind), "east")
		if d := mount.Status.Display; !strings.Contains(d.RightAscension, "h") || !strings.Contains(d.Declination, "°") {
			t.Errorf("mount display = %+v", d)
		}
		tube, _ := decode[observatory.OpticalTube](t, w.api, kindCollection(observatory.OpticalTubeKind), "east-refractor")
		if d := tube.Status.Display; d.Aperture != "80 mm" || d.FocalLength != "480 mm" {
			t.Errorf("tube display = %+v", d)
		}
		lab, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
		if d := lab.Status.Display; d.Latitude != "30.169° S" || d.Longitude != "70.806° W" {
			t.Errorf("observatory display = %+v", d)
		}
		east, _ := decode[observatory.Telescope](t, w.api, kindCollection(observatory.TelescopeKind), "east")
		if east.Status.Display.Guider != "Ready, Stopped" {
			t.Errorf("east guider column = %q", east.Status.Display.Guider)
		}
		power, _ := decode[observatory.Switch](t, w.api, kindCollection(observatory.SwitchKind), "east")
		if power.Status.Display.On != "1, 2, 3" {
			t.Errorf("switch display = %+v, want outputs 1, 2, 3 on", power.Status.Display)
		}
		// The west camera has no pod. It shows no temperature, and
		// its spec states no setpoint.
		west, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "west-main")
		if west.Status.Display.Temperature != "" {
			t.Errorf("west camera display = %+v", west.Status.Display)
		}
	})
}
