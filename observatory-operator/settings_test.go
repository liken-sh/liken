package main

// Configure writes each setting that a spec states to the property its
// field names, in the shape of that property: a switch's member, a
// switch's members that are on, an object of a property's members, a
// list of the driver's elements, and one property of a family.

import (
	"maps"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestConfigureWritesEachSettingInItsPropertysShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		shape  string
		kind   observatory.Kind
		name   string
		fields map[string]any
		want   string
	}{
		{"a switch's member", observatory.MountKind, "east", map[string]any{"trackMode": "TRACK_LUNAR"},
			"east-telescope Telescope Simulator.TELESCOPE_TRACK_MODE TRACK_SIDEREAL=Off TRACK_SOLAR=Off TRACK_LUNAR=On TRACK_CUSTOM=Off"},
		{"the members of a switch that are on", observatory.MountKind, "east", map[string]any{"reverseMotion": []any{"REVERSE_WE"}},
			"east-telescope Telescope Simulator.TELESCOPE_REVERSE_MOTION REVERSE_NS=Off REVERSE_WE=On"},
		{"the driver's number elements", observatory.MountKind, "east", map[string]any{"parkPosition": []any{map[string]any{"name": "PARK_DEC", "value": 45}}},
			"east-telescope Telescope Simulator.TELESCOPE_PARK_POSITION PARK_HA=-6 PARK_DEC=45"},
		{"an object of a property's members", observatory.CameraKind, "east-main", map[string]any{"temperatureRamp": map[string]any{"slope": 2, "threshold": 0.5}},
			"east-telescope CCD Simulator.CCD_TEMP_RAMP RAMP_SLOPE=2 RAMP_THRESHOLD=0.5"},
		{"a number with one member", observatory.WeatherStationKind, "lab", map[string]any{"updatePeriod": 30},
			"lab-observatory Weather Simulator.WEATHER_UPDATE PERIOD=30"},
		{"one property of a family", observatory.WeatherStationKind, "lab", map[string]any{"parameterLimits": []any{map[string]any{"name": "WEATHER_TEMPERATURE", "minimum": -20, "maximum": 30}}},
			"lab-observatory Weather Simulator.WEATHER_TEMPERATURE MIN_OK=-20 MAX_OK=30"},
		{"the driver's text elements", observatory.SwitchKind, "east", map[string]any{"outputLabels": []any{"Camera", "Hub"}},
			"east-telescope Simulator IO.DIGITAL_OUTPUT_LABELS DIGITAL_OUTPUT_1=Camera DIGITAL_OUTPUT_2=Hub"},
	}
	for _, c := range cases {
		t.Run(c.shape, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.amend(c.kind, c.name, func(spec map[string]any) { maps.Copy(spec, c.fields) })
				readyOf(w)
				if changes := strings.Join(w.indi.changes(), "\n"); !strings.Contains(changes, c.want) {
					t.Errorf("changes = %s\nwant %q", changes, c.want)
				}
			})
		})
	}
}

// A setting goes once, when the reservation configures the device: a
// client that changes it later, such as a sequencer that sets the gain
// for one frame, keeps its change.
func TestASettingIsWrittenOnceForAReservation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		w.indi.report("CCD Simulator", "CCD_GAIN", "GAIN", "50")
		time.Sleep(time.Hour)
		synctest.Wait()
		if n := w.indi.count("east-telescope", "CCD Simulator.CCD_GAIN"); n != 1 {
			t.Errorf("CCD_GAIN received %d changes, want the Configure step's one", n)
		}
	})
}

// A setting that names a property or a member that the driver does not
// define is noted in the step's summary, and the step goes on.
func TestASettingTheDriverLacksIsNoted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		kind   observatory.Kind
		device string
		fields map[string]any
		step   observatory.StepName
		note   string
	}{
		{"a property of a family", observatory.WeatherStationKind, "lab",
			map[string]any{"parameterLimits": []any{map[string]any{"name": "WEATHER_SNOW", "minimum": 0}}},
			observatory.StepStartSite, "no WEATHER_SNOW on WeatherStation lab"},
		{"an element of a number", observatory.MountKind, "east",
			map[string]any{"parkPosition": []any{map[string]any{"name": "PARK_AZ", "value": 10}}},
			observatory.StepConfigure, "no member PARK_AZ of TELESCOPE_PARK_POSITION on Mount east"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := startWorld(t)
				w.amend(c.kind, c.device, func(spec map[string]any) { maps.Copy(spec, c.fields) })
				r := readyOf(w)
				if summary := stepOf(r, c.step).Summary; !strings.Contains(summary, c.note) {
					t.Errorf("%s = %q, want %q", c.step, summary, c.note)
				}
			})
		})
	}
}
