package observatory

import (
	"encoding/json"
	"testing"
)

func TestAnObjectReturnsItsMetadata(t *testing.T) {
	mount := &Mount{Metadata: ObjectMeta{Name: "east-mount", Namespace: "observatory", ResourceVersion: "42"}}
	meta := mount.GetObjectMeta()
	got := []string{meta.GetNamespace(), meta.GetName(), meta.GetResourceVersion()}
	want := []string{"observatory", "east-mount", "42"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("metadata = %v, want %v", got, want)
		}
	}
}

func TestAKindReturnsTheURLOfItsCollection(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{MountKind, "/apis/observatory.liken.sh/v1alpha1/namespaces/observatory/mounts"},
		{GPSKind, "/apis/observatory.liken.sh/v1alpha1/namespaces/observatory/gpses"},
		{ReservationKind, "/apis/observatory.liken.sh/v1alpha1/namespaces/observatory/reservations"},
	}
	for _, c := range cases {
		if got := c.kind.Path("observatory"); got != c.want {
			t.Errorf("%s path = %q, want %q", c.kind.Name, got, c.want)
		}
	}
}

func TestKindNamedFindsAKindByName(t *testing.T) {
	cases := []struct {
		name  string
		want  Kind
		found bool
	}{
		{"Mount", MountKind, true},
		{"WeatherStation", WeatherStationKind, true},
		{"Telescope", TelescopeKind, true},
		{"mount", Kind{}, false},
		{"Planet", Kind{}, false},
	}
	for _, c := range cases {
		if got, found := KindNamed(c.name); got != c.want || found != c.found {
			t.Errorf("KindNamed(%q) = %v %t, want %v %t", c.name, got, found, c.want, c.found)
		}
	}
}

// A device's spec embeds the struct with its parent field, such as
// TrainDevice, and that struct's Parent method returns the zero Parent
// when the field is empty.
func TestADeviceReturnsItsParent(t *testing.T) {
	cases := []struct {
		name   string
		device interface{ Parent() Parent }
		want   Parent
	}{
		{"a mount", MountSpec{TelescopeDevice: TelescopeDevice{Telescope: "east"}}, Parent{TelescopeKind, "east"}},
		{"a camera", CameraSpec{TrainDevice: TrainDevice{OpticalTrain: "east-imaging"}},
			Parent{OpticalTrainKind, "east-imaging"}},
		{"a dome", DomeSpec{ObservatoryDevice: ObservatoryDevice{Observatory: "lab"}}, Parent{ObservatoryKind, "lab"}},
		{"a switch on a telescope", SwitchSpec{TelescopeOrObservatoryDevice: TelescopeOrObservatoryDevice{Telescope: "east"}},
			Parent{TelescopeKind, "east"}},
		{"a switch on the observatory", SwitchSpec{TelescopeOrObservatoryDevice: TelescopeOrObservatoryDevice{Observatory: "lab"}},
			Parent{ObservatoryKind, "lab"}},
		{"a mount with no parent", MountSpec{}, Parent{}},
		{"a camera with no parent", CameraSpec{}, Parent{}},
		{"a dome with no parent", DomeSpec{}, Parent{}},
		{"a switch with no parent", SwitchSpec{}, Parent{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.device.Parent(); got != c.want {
				t.Errorf("parent = %+v, want %+v", got, c.want)
			}
		})
	}
}

// The embedded structs marshal at the top level of the spec, where the
// CRD declares their fields.
func TestTheSharedDeviceFieldsAreAtTheTopOfTheSpec(t *testing.T) {
	spec := CameraSpec{TrainDevice: TrainDevice{
		OpticalTrain: "east-imaging",
		DeviceSpec: DeviceSpec{
			Driver: Driver{Name: "indi_simulator_ccd"},
			Power:  &Power{Switch: "east-power", Output: 1},
		},
	}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"opticalTrain":"east-imaging","driver":{"name":"indi_simulator_ccd"},"power":{"switch":"east-power","output":1}}`
	if string(raw) != want {
		t.Errorf("spec = %s, want %s", raw, want)
	}
}
