package main

// Each property of an INDI device is in the status of one resource: a
// standard property in the typed fields of the resource whose interface
// defines it, and every other property in status.properties of the
// resource that runs the driver. The example's FilterWheel east is
// served by Camera east-main, whose CCD simulator defines the wheel's
// properties beside its own.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// propertyNames answers the names in a status's property list.
func propertyNames(list []observatory.Property) []string {
	var names []string
	for _, p := range list {
		names = append(names, p.Name)
	}
	return names
}

// A new slot moves the FilterWheel, and the camera that serves it holds
// nothing of the wheel, so its status is not written again.
func TestAFilterSlotUpdateWritesTheFilterWheelAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		cameras, wheels := kindCollection(observatory.CameraKind), kindCollection(observatory.FilterWheelKind)
		camera, wheel := w.api.statusWrites(cameras, "east-main"), w.api.statusWrites(wheels, "east")
		w.indi.report("CCD Simulator", "FILTER_SLOT", "FILTER_SLOT_VALUE", "3")
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		if writes := w.api.statusWrites(cameras, "east-main") - camera; writes != 0 {
			t.Errorf("%d writes of Camera east-main for a new slot, want none", writes)
		}
		if writes := w.api.statusWrites(wheels, "east") - wheel; writes != 1 {
			t.Errorf("%d writes of FilterWheel east for a new slot, want 1", writes)
		}
		got, _ := decode[observatory.FilterWheel](t, w.api, wheels, "east")
		if got.Status.Slot == nil || got.Status.Slot.Value == nil || *got.Status.Slot.Value != 3 || got.Status.Readings.Filter != "Green" {
			t.Errorf("filter wheel slot = %+v, readings = %+v, want slot 3, Green", got.Status.Slot, got.Status.Readings)
		}
	})
}

// The CCD simulator's own properties, such as SIMULATOR_SETTINGS, and
// DefaultDevice's DEBUG are in the camera's list alone. The camera's
// standard properties are typed fields, and the wheel's standard
// properties are the FilterWheel's.
func TestAVendorPropertyIsInTheOwnersListAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		names := propertyNames(camera.Status.Properties)
		for _, want := range []string{"SIMULATOR_SETTINGS", "EQUATORIAL_PE", "DEBUG", "CONNECTION"} {
			if !slices.Contains(names, want) {
				t.Errorf("Camera east-main's properties = %v, want %s", names, want)
			}
		}
		for _, typed := range []string{"CCD_GAIN", "CCD_TEMPERATURE", "FILTER_SLOT", "FILTER_NAME"} {
			if slices.Contains(names, typed) {
				t.Errorf("Camera east-main's properties hold %s, which a typed field holds", typed)
			}
		}
		wheel, _ := w.api.object(kindCollection(observatory.FilterWheelKind), "east")
		body, _ := json.Marshal(wheel["status"])
		for _, vendor := range []string{"SIMULATOR_SETTINGS", "EQUATORIAL_PE", "DEBUG", "\"properties\""} {
			if strings.Contains(string(body), vendor) {
				t.Errorf("FilterWheel east's status holds %s: %s", vendor, body)
			}
		}
	})
}

// A camera's wheel with no FilterWheel to hold it stays in the camera's
// list, so a person still reads it.
func TestAWheelThatNoResourceIsStaysInTheCamerasList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.deleteNamed(kindCollection(observatory.FilterWheelKind), "east")
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		names := propertyNames(camera.Status.Properties)
		if !slices.Contains(names, "FILTER_SLOT") || !slices.Contains(names, "FILTER_NAME") {
			t.Errorf("Camera east-main's properties = %v, want FILTER_SLOT and FILTER_NAME", names)
		}
	})
}

// heldBy answers the standard properties that a transcript defines, by
// the JSON name of the typed field of a kind that holds each one: the
// property that the field's tag names, each numbered property of a
// family, or each property that a member of the kind's NamedBy names.
func heldBy(t *testing.T, kind observatory.Kind, simulator string) map[string][]string {
	t.Helper()
	dir := filepath.Join("indi", "testdata", simulator)
	defined := map[string]*fakeProperty{}
	var order []string
	for _, file := range []string{"baseline.xml", "connect.xml"} {
		for _, p := range definitions(t, filepath.Join(dir, file)) {
			if _, seen := defined[p.Name]; !seen {
				order = append(order, p.Name)
			}
			defined[p.Name] = p
		}
	}
	out := map[string][]string{}
	for f := range reflect.TypeOf(observatory.Standards[kind].Status()).Elem().Fields() {
		field, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		tag := f.Tag.Get("indi")
		_, named := f.Type.Elem().FieldByName("Name")
		switch {
		case f.Type.Kind() == reflect.Slice && named:
			for _, by := range observatory.Standards[kind].NamedBy {
				for _, m := range defined[by].Members {
					if defined[m.Name] != nil {
						out[field] = append(out[field], m.Name)
					}
				}
			}
		case f.Type.Kind() == reflect.Slice:
			for _, name := range order {
				if _, numbered := familyNumber(name, strings.TrimRight(tag, "0123456789")); numbered {
					out[field] = append(out[field], name)
				}
			}
		case defined[tag] != nil:
			out[field] = []string{tag}
		}
	}
	return out
}

// Each typed field holds the property that the simulator defines, for
// each kind, and no list holds a property that a typed field holds.
func TestEachTypedFieldHoldsWhatTheSimulatorDefines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind            observatory.Kind
		name, simulator string
	}{
		{observatory.MountKind, "east", "telescope"},
		{observatory.CameraKind, "east-main", "ccd"},
		{observatory.CameraKind, "east-guide", "guide"},
		{observatory.FilterWheelKind, "east", "ccd"},
		{observatory.FocuserKind, "east", "focus"},
		{observatory.RotatorKind, "east", "rotator"},
		{observatory.DustCapKind, "east", "dustcover"},
		{observatory.FlatPanelKind, "east", "lightpanel"},
		{observatory.PolarAlignerKind, "east", "pac"},
		{observatory.GPSKind, "east", "gps"},
		{observatory.DomeKind, "lab", "dome"},
		{observatory.WeatherStationKind, "lab", "weather"},
		{observatory.SwitchKind, "east", "io"},
		{observatory.ReceiverKind, "east", "receiver"},
	}
	var statuses map[string]map[string]any
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		statuses = map[string]map[string]any{}
		for _, c := range cases {
			object, _ := w.api.object(kindCollection(c.kind), c.name)
			statuses[c.kind.Name+"/"+c.name] = object["status"].(map[string]any)
		}
	})
	for _, c := range cases {
		t.Run(c.kind.Name+"/"+c.name, func(t *testing.T) {
			status := statuses[c.kind.Name+"/"+c.name]
			held := heldBy(t, c.kind, c.simulator)
			if len(held) == 0 {
				t.Fatalf("the %s transcript defines no standard property of a %s", c.simulator, c.kind.Name)
			}
			listed, _ := json.Marshal(status["properties"])
			for field, names := range held {
				value, present := status[field]
				if !present {
					t.Errorf("status.%s is absent, and the simulator defines %v", field, names)
				}
				if items, family := value.([]any); family && len(items) != len(names) {
					t.Errorf("status.%s holds %d items, and the simulator defines %v", field, len(items), names)
				}
				for _, name := range names {
					if strings.Contains(string(listed), `"name":"`+name+`"`) {
						t.Errorf("status.properties holds %s, which status.%s holds", name, field)
					}
				}
			}
		})
	}
}

// A reserved camera's status holds each definition once: the schema
// describes each typed field, and the status holds its value. On the
// CCD simulator's transcripts, Camera east-main writes 12.5 KB: 3.1 KB
// of typed fields, 1.4 KB of limits, 6.7 KB of the simulator's own
// properties and DefaultDevice's in status.properties, and 1.3 KB of
// the rest. With every property in status.properties, it wrote 23.6 KB.
func TestAReservedCamerasStatusHoldsEachDefinitionOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		camera, _ := w.api.object(kindCollection(observatory.CameraKind), "east-main")
		body, _ := json.Marshal(camera["status"])
		if len(body) > 13000 {
			t.Errorf("Camera east-main's status is %d bytes, want at most 13,000", len(body))
		}
	})
}

// A mount that serves a GPS gives GEOGRAPHIC_COORD, which both standard
// sets hold, to the GPS: the served resource exists only to show its
// interface, and the mount keeps the rest.
func TestAPropertyOfTwoInterfacesGoesToTheServedResource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.put(observatory.MountKind, "east", map[string]any{"telescope": "east",
			"driver": map[string]any{"name": "indi_fake_gpsmount", "image": variantImage}})
		w.put(observatory.GPSKind, "east", map[string]any{"telescope": "east",
			"device": map[string]any{"kind": "Mount", "name": "east"}})
		readyOf(w)
		gps, _ := decode[observatory.GPS](t, w.api, kindCollection(observatory.GPSKind), "east")
		if gps.Status.Location == nil || gps.Status.Location.Latitude == nil {
			t.Errorf("the GPS's location = %+v, want GEOGRAPHIC_COORD", gps.Status.Location)
		}
		mount, _ := decode[observatory.Mount](t, w.api, kindCollection(observatory.MountKind), "east")
		if mount.Status.Location != nil || slices.Contains(propertyNames(mount.Status.Properties), "GEOGRAPHIC_COORD") {
			t.Errorf("the mount holds GEOGRAPHIC_COORD: location %+v", mount.Status.Location)
		}
		if mount.Status.Equatorial == nil {
			t.Error("the mount lost EQUATORIAL_EOD_COORD")
		}
	})
}

// shelveWheel puts the example's FilterWheel east on the shelf during a
// session, and waits until its deactivation ran and it let go of the
// camera that served it.
func shelveWheel(w *world) {
	w.t.Helper()
	w.put(observatory.FilterWheelKind, "east", map[string]any{
		"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"L"}})
	w.until(5*time.Minute, "the shelved filter wheel keeps its finalizer", func() bool {
		wheel, _ := decode[observatory.FilterWheel](w.t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		return !holdsFinalizer(wheel.Metadata)
	})
}

// A served device on the shelf that let go of its owner reads nothing
// of the owner's INDI device: it is Inventory, and its wheel's
// properties go back to the camera's list.
func TestAServedDeviceOnTheShelfReadsNothingOfItsOwner(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		shelveWheel(w)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if s := wheel.Status; s.Phase != observatory.DeviceInventory || s.IndiDevice != "" || s.Endpoint != nil || s.Slot != nil {
			t.Errorf("the shelved filter wheel = %s on %q at %+v, slot %+v", s.Phase, s.IndiDevice, s.Endpoint, s.Slot)
		}
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if names := propertyNames(camera.Status.Properties); !slices.Contains(names, "FILTER_SLOT") {
			t.Errorf("Camera east-main's properties = %v, want FILTER_SLOT", names)
		}
	})
}

// A served device that joins its owner takes the owner's properties
// from one of its kind on the shelf, whatever the order of their names.
func TestAPlacedServedDeviceTakesThePropertiesFromOneOnTheShelf(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		shelveWheel(w)
		w.put(observatory.FilterWheelKind, "east-new", map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"L"}})
		w.until(time.Minute, "the new filter wheel does not read the camera's wheel", func() bool {
			wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-new")
			return wheel.Status.Slot != nil
		})
		shelved, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if shelved.Status.Slot != nil || shelved.Status.IndiDevice != "" {
			t.Errorf("the shelved filter wheel reads slot %+v on %q", shelved.Status.Slot, shelved.Status.IndiDevice)
		}
	})
}
