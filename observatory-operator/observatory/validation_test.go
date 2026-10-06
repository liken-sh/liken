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
		{"a cooler setpoint below the limit", CameraKind,
			map[string]any{"opticalTrain": "east-imaging", "driver": simulator("indi_simulator_ccd"),
				"temperature": -150.0}, "spec.temperature: Invalid value"},
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
