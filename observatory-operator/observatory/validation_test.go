package observatory

// Each case is a resource that a person could write, and the verdict
// that the API server gives it. The tests run the structural schema
// validator and then the CEL rules, the two passes the API server
// runs on a create or an update.

import (
	"strings"
	"testing"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
)

// validate answers the errors that the API server would give an
// object of the kind. With an old object, it is an update, and the
// rules that compare with oldSelf run too.
func validate(t *testing.T, kind Kind, object, old map[string]any) field.ErrorList {
	t.Helper()
	internal := internalSchema(t, kind)
	schemaValidator, _, err := validation.NewSchemaValidator(internal)
	if err != nil {
		t.Fatal(err)
	}
	errs := validation.ValidateCustomResource(field.NewPath(""), object, schemaValidator)
	structural, err := structuralschema.NewStructural(internal)
	if err != nil {
		t.Fatal(err)
	}
	celErrs, _ := celschema.NewValidator(structural, true, celconfig.PerCallLimit).Validate(
		t.Context(), field.NewPath(""), structural, object, old, celconfig.RuntimeCELCostBudget)
	return append(errs, celErrs...)
}

// resource builds an object of a kind with the spec given.
func resource(kind Kind, spec map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": APIVersion,
		"kind":       kind.Name,
		"metadata":   map[string]any{"name": "test", "namespace": "observatory"},
		"spec":       spec,
	}
}

func simulator(name string) map[string]any {
	return map[string]any{"name": name}
}

// camera builds the spec of a camera on a train, with more fields.
func camera(more map[string]any) map[string]any {
	spec := map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_ccd")}
	for k, v := range more {
		spec[k] = v
	}
	return spec
}

func TestTheAPIServerAcceptsAndRefuses(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		spec map[string]any
		// refusal is part of the error, or empty when the resource is
		// accepted.
		refusal string
	}{
		{"a camera on a train with a simulator", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_ccd")}, ""},
		{"a camera on the shelf", CameraKind,
			map[string]any{"driver": simulator("indi_simulator_ccd")}, ""},
		{"a camera with no driver", CameraKind,
			map[string]any{"opticalTrain": "east-imaging"}, "spec.driver: Required value"},
		{"a driver that is not an INDI driver, with no image", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("ccd")},
			"driver.name must match ^indi_[a-z0-9_]+$"},
		{"a driver of another name in its own image", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{
				"name": "acme-ccd", "image": "ghcr.io/example/acme-ccd:1.0"}}, ""},
		{"a driver name with a path", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": map[string]any{
				"name": "/usr/bin/indi_simulator_ccd", "image": "ghcr.io/example/ccd:1.0"}},
			"spec.driver.name: Invalid value"},
		{"a camera that cools on activation and warms on deactivation", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"cool": map[string]any{"celsius": -10.0}}},
				"deactivation": []any{map[string]any{"warm": map[string]any{"celsius": 5.0, "within": 1.0}, "timeout": "15m"}}}), ""},
		{"a cooler setpoint below the limit", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"cool": map[string]any{"celsius": -150.0}}}}),
			"spec.activation[0].cool.celsius: Invalid value"},
		{"an action that cools and warms", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"cool": map[string]any{"celsius": -10.0}, "warm": map[string]any{"celsius": 5.0}}}}),
			"an action names exactly one of cool, warm, and job"},
		{"an action that names nothing", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"timeout": "5m"}}}),
			"an action names exactly one of cool, warm, and job"},
		{"a park that a camera cannot do", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"state": "Parked"}}}),
			"an action names exactly one of cool, warm, and job"},
		{"a dome that unparks in safe weather", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"activation": []any{map[string]any{"state": "Unparked",
					"requires": []any{map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe"}}}},
				"deactivation": []any{map[string]any{"state": "Parked", "after": []any{map[string]any{"kind": "Mount"}}}}}, ""},
		{"a dome that opens like a dust cap", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"activation": []any{map[string]any{"state": "Open"}}}, "Unsupported value"},
		{"a timeout that is no duration", MountKind,
			map[string]any{"telescope": "east", "driver": simulator("indi_simulator_telescope"),
				"activation": []any{map[string]any{"state": "Unparked", "timeout": "soon"}}},
			"timeout must be a positive duration"},
		{"a timeout of nothing", MountKind,
			map[string]any{"telescope": "east", "driver": simulator("indi_simulator_telescope"),
				"activation": []any{map[string]any{"state": "Unparked", "timeout": "0s"}}},
			"timeout must be a positive duration"},
		{"a requirement on a Reservation", MountKind,
			map[string]any{"telescope": "east", "driver": simulator("indi_simulator_telescope"),
				"activation": []any{map[string]any{"state": "Unparked",
					"requires": []any{map[string]any{"kind": "Reservation", "name": "x", "type": "Ready"}}}}},
			"spec.activation[0].requires[0].kind: Unsupported value"},
		{"a dust cap that closes", DustCapKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_dustcover"),
				"deactivation": []any{map[string]any{"state": "Closed"}}}, ""},
		{"a flat panel that switches its light off", FlatPanelKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_lightpanel"),
				"deactivation": []any{map[string]any{"state": "Dark"}}}, ""},
		{"a flat panel state that YAML reads as a boolean", FlatPanelKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_lightpanel"),
				"deactivation": []any{map[string]any{"state": "Off"}}}, "spec.deactivation[0].state: Unsupported value"},
		{"a dome that parks in bad weather and unparks after 20 minutes of good", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"triggers": []any{
					map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"},
						"run": []any{map[string]any{"state": "Parked", "after": []any{map[string]any{"kind": "Mount"}}}}},
					map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "for": "20m"},
						"run": []any{map[string]any{"state": "Unparked"}}},
				}}, ""},
		{"a trigger with no actions", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"triggers": []any{map[string]any{"when": map[string]any{"type": "Parked"}, "run": []any{}}}},
			"spec.triggers[0].run: Invalid value"},
		{"a trigger that waits for a negative time", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"triggers": []any{map[string]any{"when": map[string]any{"type": "Parked", "for": "-5m"},
					"run": []any{map[string]any{"state": "Unparked"}}}}},
			"for must be a duration"},
		{"a trigger with no condition type", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"triggers": []any{map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "lab"},
					"run": []any{map[string]any{"state": "Parked"}}}}},
			"spec.triggers[0].when.type: Required value"},
		{"a focuser's action that names nothing", FocuserKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_focus"),
				"activation": []any{map[string]any{"timeout": "1m"}}}, "an action of a Focuser names job"},
		{"a focuser's job", FocuserKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_focus"),
				"activation": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37", "command": []any{"sh", "-c", "env"},
					"env": []any{map[string]any{"name": "RELAY_URL", "value": "http://relay.local/1"}}}}}}, ""},
		{"an observatory's job on a trigger", ObservatoryKind,
			map[string]any{"location": map[string]any{"latitude": -30.169, "longitude": -70.806, "elevation": 2207},
				"triggers": []any{map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"},
					"run": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37"}, "timeout": "2m"}}}}}, ""},
		{"a dome's action that parks and runs a job", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"deactivation": []any{map[string]any{"state": "Parked", "job": map[string]any{"image": "busybox:1.37"}}}},
			"an action of a Dome names exactly one of state and job"},
		{"a job with no image", TelescopeKind,
			map[string]any{"observatory": "lab", "activation": []any{map[string]any{"job": map[string]any{"args": []any{"x"}}}}},
			"spec.activation[0].job.image: Required value"},
		{"a job variable with a space in its name", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_ccd"),
				"activation": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37", "env": []any{map[string]any{"name": "RELAY URL", "value": ""}}}}}},
			"spec.activation[0].job.env[0].name: Invalid value"},
		{"a mount on the shelf", MountKind,
			map[string]any{"driver": simulator("indi_simulator_telescope")}, ""},
		{"a parent name in capitals", MountKind,
			map[string]any{"telescope": "East", "driver": simulator("indi_simulator_telescope")},
			"spec.telescope: Invalid value"},
		{"a focuser powered by output 2", FocuserKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_focus"),
				"power": map[string]any{"switch": "east-power", "output": 2}}, ""},
		{"a focuser powered by output 0", FocuserKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_focus"),
				"power": map[string]any{"switch": "east-power", "output": 0}},
			"spec.power.output: Invalid value"},
		{"a switch on a telescope", SwitchKind,
			map[string]any{"telescope": "east", "driver": simulator("indi_simulator_io")}, ""},
		{"a switch on the observatory", SwitchKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_io")}, ""},
		{"a switch with two parents", SwitchKind,
			map[string]any{"telescope": "east", "observatory": "lab", "driver": simulator("indi_simulator_io")},
			"set at most one parent"},
		{"a switch on the shelf", SwitchKind,
			map[string]any{"driver": simulator("indi_simulator_io")}, ""},
		{"a dome on the shelf", DomeKind,
			map[string]any{"driver": simulator("indi_simulator_dome")}, ""},
		{"a shelved camera with a claim and a power output", CameraKind,
			map[string]any{"driver": simulator("indi_asi_ccd"),
				"power": map[string]any{"switch": "east", "output": 1},
				"claim": map[string]any{"devices": map[string]any{"requests": []any{
					map[string]any{"name": "camera", "exactly": map[string]any{"deviceClassName": "usb"}}}}}}, ""},
		{"a device with a claim", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_asi_ccd"),
				"claim": map[string]any{"devices": map[string]any{"requests": []any{
					map[string]any{"name": "camera", "exactly": map[string]any{"deviceClassName": "usb"}}}}}}, ""},
		{"a filter wheel with an empty filter name", FilterWheelKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_wheel"),
				"filters": []any{"Red", ""}}, "spec.filters[1]: Invalid value"},
		{"an observatory north of the pole", ObservatoryKind,
			map[string]any{"location": map[string]any{"latitude": 91.0, "longitude": 0.0, "elevation": 0.0}},
			"spec.location.latitude: Invalid value"},
		{"an observatory with no location", ObservatoryKind,
			map[string]any{}, "spec.location: Required value"},
		{"a tube with no aperture", OpticalTubeKind,
			map[string]any{"telescope": "east", "aperture": 0.0, "focalLength": 480.0},
			"spec.aperture: Invalid value"},
		{"a train with no tube", OpticalTrainKind,
			map[string]any{"telescope": "east"}, "spec.opticalTube: Required value"},
		{"a guider that pulses the mount", GuiderKind,
			map[string]any{"telescope": "east", "opticalTrain": "east-guiding", "pulses": "Mount"}, ""},
		{"a guider with a pulse target in lower case", GuiderKind,
			map[string]any{"telescope": "east", "opticalTrain": "east-guiding", "pulses": "mount"},
			"spec.pulses: Unsupported value"},
		{"a reservation with no times", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop"}, ""},
		{"a reservation that ends after it starts", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop",
				"start": "2026-10-05T20:00:00-04:00", "end": "2026-10-06T05:00:00-04:00"}, ""},
		{"a reservation that ends before it starts", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop",
				"start": "2026-10-06T05:00:00-04:00", "end": "2026-10-05T20:00:00-04:00"},
			"spec.end must be after spec.start"},
		{"a reservation with no holder", ReservationKind,
			map[string]any{"telescope": "east"}, "spec.holder: Required value"},
		{"a reservation with a time that is not RFC 3339", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop", "end": "tomorrow"},
			"spec.end: Invalid value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validate(t, c.kind, resource(c.kind, c.spec), nil)
			if got := errs.ToAggregate(); !refuses(got, c.refusal) {
				t.Errorf("errors = %v, want %q", got, c.refusal)
			}
		})
	}
}

// refuses answers whether the errors match a case's verdict: no error
// when the case states no refusal, or an error that holds its text.
func refuses(errs error, refusal string) bool {
	if errs == nil {
		return refusal == ""
	}
	return refusal != "" && strings.Contains(errs.Error(), refusal)
}

// A reservation is for one telescope. Moving it to another telescope
// while the first one is active would leave the first one running, so
// the rule refuses the change, and the person creates another
// reservation.
func TestAReservationKeepsItsTelescope(t *testing.T) {
	cases := []struct {
		name, telescope, refusal string
	}{
		{"the same telescope", "east", ""},
		{"another telescope", "west", "spec.telescope cannot change"},
	}
	old := resource(ReservationKind, map[string]any{"telescope": "east", "holder": "desktop"})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			updated := resource(ReservationKind, map[string]any{"telescope": c.telescope, "holder": "session"})
			errs := validate(t, ReservationKind, updated, old)
			if got := errs.ToAggregate(); !refuses(got, c.refusal) {
				t.Errorf("errors = %v, want %q", got, c.refusal)
			}
		})
	}
}
