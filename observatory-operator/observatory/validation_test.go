package observatory

// Each case is a resource that a person could write, and the verdict
// that the API server gives it. The tests run the structural schema
// validator and then the CEL rules, the two passes the API server
// runs on a create or an update.

import (
	"slices"
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
			map[string]any{"opticalTrain": "east-imaging"}, "set exactly one of spec.driver and spec.device"},
		{"a filter wheel that a camera serves", FilterWheelKind,
			map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Camera", "name": "east-main"},
				"filters": []any{"L", "R", "G", "B"}}, ""},
		{"a filter wheel with a driver and a serving camera", FilterWheelKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_wheel"),
				"device": map[string]any{"kind": "Camera", "name": "east-main"}},
			"set exactly one of spec.driver and spec.device"},
		{"a served dust cap with power of its own", DustCapKind,
			map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "FlatPanel", "name": "east"},
				"power": map[string]any{"switch": "east", "output": 3}},
			"spec.power and spec.claim belong to the resource that runs the driver"},
		{"a served dust cap with a claim of its own", DustCapKind,
			map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "FlatPanel", "name": "east"},
				"claim": map[string]any{}},
			"spec.power and spec.claim belong to the resource that runs the driver"},
		{"a device served by a kind that does not exist", FilterWheelKind,
			map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Telescope", "name": "east"}},
			"spec.device.kind: Unsupported value"},
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
			"an action names exactly one of cool, warm, job, and notify"},
		{"an action that names nothing", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"timeout": "5m"}}}),
			"an action names exactly one of cool, warm, job, and notify"},
		{"a park that a camera cannot do", CameraKind,
			camera(map[string]any{"activation": []any{map[string]any{"state": "Parked"}}}),
			"an action names exactly one of cool, warm, job, and notify"},
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
				"activation": []any{map[string]any{"timeout": "1m"}}}, "an action of a Focuser names exactly one of job and notify"},
		{"a focuser's job", FocuserKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_focus"),
				"activation": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37.0", "command": []any{"sh", "-c", "env"},
					"env": []any{map[string]any{"name": "RELAY_URL", "value": "http://relay.local/1"}}}}}}, ""},
		{"an observatory's job on a trigger", ObservatoryKind,
			map[string]any{"location": map[string]any{"latitude": -30.169, "longitude": -70.806, "elevation": 2207},
				"triggers": []any{map[string]any{"when": map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe", "status": "False"},
					"run": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37.0"}, "timeout": "2m"}}}}}, ""},
		{"a dome's action that parks and runs a job", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"deactivation": []any{map[string]any{"state": "Parked", "job": map[string]any{"image": "busybox:1.37.0"}}}},
			"an action of a Dome names exactly one of state, job, and notify"},
		{"a job with no image", TelescopeKind,
			map[string]any{"observatory": "lab", "activation": []any{map[string]any{"job": map[string]any{"args": []any{"x"}}}}},
			"spec.activation[0].job.image: Required value"},
		{"a job variable with a space in its name", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_ccd"),
				"activation": []any{map[string]any{"job": map[string]any{"image": "busybox:1.37.0", "env": []any{map[string]any{"name": "RELAY URL", "value": ""}}}}}},
			"spec.activation[0].job.env[0].name: Invalid value"},
		{"a portable telescope that waits for a person and asks to be covered", TelescopeKind,
			map[string]any{"observatory": "lab",
				"requires":     []any{map[string]any{"type": "SetUp"}, map[string]any{"kind": "WeatherStation", "name": "lab", "type": "Safe"}},
				"deactivation": []any{map[string]any{"notify": "Cover the telescope and carry it in"}}}, ""},
		{"a requirement with no condition type", TelescopeKind,
			map[string]any{"observatory": "lab", "requires": []any{map[string]any{"kind": "WeatherStation", "name": "lab"}}},
			"spec.requires[0].type: Required value"},
		{"a requirement on a Reservation", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"requires": []any{map[string]any{"kind": "Reservation", "name": "x", "type": "Ready"}}},
			"spec.requires[0].kind: Unsupported value"},
		{"an empty request to a person", TelescopeKind,
			map[string]any{"observatory": "lab", "deactivation": []any{map[string]any{"notify": ""}}},
			"spec.deactivation[0].notify: Invalid value"},
		{"a dome's action that parks and asks a person", DomeKind,
			map[string]any{"observatory": "lab", "driver": simulator("indi_simulator_dome"),
				"deactivation": []any{map[string]any{"state": "Parked", "notify": "Close the roof"}}},
			"an action of a Dome names exactly one of state, job, and notify"},
		{"a telescope that names its horizon", TelescopeKind,
			map[string]any{"observatory": "lab", "horizon": "yard"}, ""},
		{"a horizon name in capitals", TelescopeKind,
			map[string]any{"observatory": "lab", "horizon": "Yard"}, "spec.horizon: Invalid value"},
		{"a horizon with no profile", HorizonKind,
			map[string]any{}, "spec.profile: Required value"},
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
		{"a reservation with an outbox", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop", "outbox": map[string]any{"claimName": "frames"}}, ""},
		{"a reservation with an outbox that names no claim", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop", "outbox": map[string]any{}},
			"spec.outbox.claimName: Required value"},
		{"an observatory with FITS keywords", ObservatoryKind,
			map[string]any{"location": map[string]any{"latitude": 51.5, "longitude": 0.0, "elevation": 40.0},
				"fitsKeywords": []any{fits("OBSERVER", "Ada Lovelace", "who took the frame"), fits("SITENAME", "Back yard", "")}}, ""},
		{"a telescope that names itself in TELESCOP", TelescopeKind,
			map[string]any{"observatory": "lab", "fitsKeywords": []any{fits("TELESCOP", "Esprit 100", "")}}, ""},
		{"a camera that names itself in INSTRUME", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("INSTRUME", "ASI2600MM Pro", "")}}), ""},
		{"a reservation with a target", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop", "fitsKeywords": []any{fits("OBJECT", "M42", ""), fits("PROG-ID", "2026_B", "")}}, ""},
		{"a keyword that the driver measures", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("EXPTIME", "300", "")}}),
			"EXPTIME is a keyword that the camera's driver writes"},
		{"a keyword that FITS reserves", ReservationKind,
			map[string]any{"telescope": "east", "holder": "desktop", "fitsKeywords": []any{fits("HISTORY", "stacked", "")}},
			"HISTORY is a keyword that FITS reserves"},
		{"a keyword name in lowercase", TelescopeKind,
			map[string]any{"observatory": "lab", "fitsKeywords": []any{fits("observer", "Ada", "")}},
			"a FITS keyword name is 1 to 8 of"},
		{"a keyword name of nine characters", TelescopeKind,
			map[string]any{"observatory": "lab", "fitsKeywords": []any{fits("OBSERVERS", "Ada", "")}},
			"spec.fitsKeywords[0].name: Too long"},
		{"a keyword with no value", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("OBSERVER", "", "")}}),
			"spec.fitsKeywords[0].value: Invalid value"},
		{"a keyword value that is not ASCII", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("OBSERVER", "Ada Lovelace °", "")}}),
			"FITS allows only the printable ASCII characters"},
		{"a value with a slash", TelescopeKind,
			map[string]any{"observatory": "lab", "fitsKeywords": []any{fits("FOCRATIO", "f/5", "")}},
			"writes each '/' in a value as"},
		{"a WCS keyword", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("CRVAL1", "83.8", "")}}),
			"CRVAL1 is a keyword that the camera's driver writes"},
		{"a keyword value longer than a card holds", CameraKind,
			camera(map[string]any{"fitsKeywords": []any{fits("NOTES", strings.Repeat("x", 69), "")}}),
			"spec.fitsKeywords[0].value: Too long"},
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

// fits builds one item of spec.fitsKeywords, with no comment when
// comment is "".
func fits(name, value, comment string) map[string]any {
	item := map[string]any{"name": name, "value": value}
	if comment != "" {
		item["comment"] = comment
	}
	return item
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

// A served resource keeps the device that serves it, and a resource
// keeps spec.driver or spec.device for its life. Moving one is a delete
// and a create, so the operator never meets a pointer that changed
// while the old device still runs it.
func TestAServedResourceKeepsItsDevice(t *testing.T) {
	served := func(camera string) map[string]any {
		return map[string]any{"opticalTrain": "east-imaging", "device": map[string]any{"kind": "Camera", "name": camera}}
	}
	driven := map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_wheel")}
	cases := []struct {
		name     string
		old, now map[string]any
		refusal  string
	}{
		{"the same device, other filters", served("east-main"), map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"L"}}, ""},
		{"another train on the same device", served("east-main"), map[string]any{"opticalTrain": "east-guiding",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}}, ""},
		{"another device", served("east-main"), served("east-guide"), "spec.device cannot change"},
		{"a driver in place of the device", served("east-main"), driven, "a resource keeps spec.driver or spec.device for its life"},
		{"a device in place of the driver", driven, served("east-main"), "a resource keeps spec.driver or spec.device for its life"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validate(t, FilterWheelKind, resource(FilterWheelKind, c.now), resource(FilterWheelKind, c.old))
			if got := errs.ToAggregate(); !refuses(got, c.refusal) {
				t.Errorf("errors = %v, want %q", got, c.refusal)
			}
		})
	}
}

// Every kind caps its name at 32 characters. observatory-operator names
// the pod and Service of a device, a Telescope, or an Observatory
// <name>-<kind>, a Service name holds at most 63 characters, and the
// longest kind, WeatherStation, leaves 48. A Job's labels carry the name
// of the resource whose trigger ran it, and a label value holds at most
// 63. One cap for every kind leaves room for a longer kind.
func TestEveryKindCapsItsName(t *testing.T) {
	cases := []struct {
		name    string
		length  int
		refused bool
	}{
		{"a name of 32 characters", 32, false},
		{"a name of 33 characters", 33, true},
	}
	for _, kind := range Kinds {
		for _, one := range cases {
			t.Run(kind.Name+"/"+one.name, func(t *testing.T) {
				object := resource(kind, map[string]any{})
				object["metadata"].(map[string]any)["name"] = strings.Repeat("n", one.length)

				refused := slices.ContainsFunc(validate(t, kind, object, nil), func(err *field.Error) bool {
					return strings.Contains(err.Error(), "name is at most 32 characters")
				})

				if refused != one.refused {
					t.Errorf("refused = %v, want %v", refused, one.refused)
				}
			})
		}
	}
}
