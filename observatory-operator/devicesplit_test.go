package main

// In the example, Camera east-main serves FilterWheel east, and the
// camera's CCD simulator defines the wheel's properties together with
// its own.

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func propertyNames(list []observatory.Property) []string {
	var names []string
	for _, p := range list {
		names = append(names, p.Name)
	}
	return names
}

// A new filter slot is written to the FilterWheel's status only. The
// camera's status does not contain the wheel's properties, so the camera
// is not written again.
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

// Vendor-specific properties such as SIMULATOR_SETTINGS, and
// DefaultDevice's DEBUG, appear only in the camera's status.properties.
// The camera's standard properties are in typed fields, and the wheel's
// standard properties are in the FilterWheel's status.
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
				t.Errorf("Camera east-main's properties include %s, want it only in a typed field", typed)
			}
		}
		wheel, _ := w.api.object(kindCollection(observatory.FilterWheelKind), "east")
		body, _ := json.Marshal(wheel["status"])
		for _, vendor := range []string{"SIMULATOR_SETTINGS", "EQUATORIAL_PE", "DEBUG", "\"properties\""} {
			if strings.Contains(string(body), vendor) {
				t.Errorf("FilterWheel east's status contains %s, want it absent: %s", vendor, body)
			}
		}
	})
}

// When no FilterWheel resource exists for a camera's wheel, the wheel's
// properties stay in the camera's status.properties, so kubectl still
// shows them.
func TestWheelPropertiesStayInTheCamerasListWithoutAFilterWheel(t *testing.T) {
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

// definedByField returns the standard properties that a simulator's
// baseline and connect transcripts define, keyed by the JSON name of the
// kind's typed field that holds them. A slice of items with a Name
// collects the properties that the members of the kind's NamedBy
// properties name. Any other slice collects the numbered family of its
// indi tag. Any other field holds the one property its indi tag names.
func definedByField(t *testing.T, kind observatory.Kind, simulator string) map[string][]string {
	t.Helper()
	defined := map[string]*inditest.Property{}
	var order []string
	for _, phase := range []string{"baseline", "connect"} {
		for _, p := range definitions(t, simulator, phase) {
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

// For each kind, each typed field is set from the properties that the
// simulator defines, and status.properties does not repeat a property
// that a typed field holds.
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
			held := definedByField(t, c.kind, c.simulator)
			if len(held) == 0 {
				t.Fatalf("the %s transcript defines no standard property of a %s", c.simulator, c.kind.Name)
			}
			listed, _ := json.Marshal(status["properties"])
			for field, names := range held {
				value, present := status[field]
				if !present {
					t.Errorf("status.%s is absent, want it to hold %v from the simulator", field, names)
				}
				if items, family := value.([]any); family && len(items) != len(names) {
					t.Errorf("status.%s holds %d items, want %d for %v from the simulator", field, len(items), len(names), names)
				}
				for _, name := range names {
					if strings.Contains(string(listed), `"name":"`+name+`"`) {
						t.Errorf("status.properties lists %s, want it only in status.%s", name, field)
					}
				}
			}
		})
	}
}

// A reserved camera's status lists each property once, and a typed field
// holds only the value, because the CRD's schema describes the field. A
// status that also repeated the typed properties in status.properties
// would exceed this limit.
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

// When a mount serves a GPS, GEOGRAPHIC_COORD goes to the GPS, though
// the standard sets of both kinds contain it.
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
			t.Errorf("the mount has location %+v or lists GEOGRAPHIC_COORD, want neither", mount.Status.Location)
		}
		if mount.Status.Equatorial == nil {
			t.Error("the mount has no equatorial coordinates, want EQUATORIAL_EOD_COORD")
		}
	})
}

// putUninstalledWheel creates the example's FilterWheel east in Inventory: it
// names its owner but no telescope or optical train, so no server
// places it. Call it before a session starts.
func putUninstalledWheel(w *world) {
	w.put(observatory.FilterWheelKind, "east", map[string]any{
		"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"L"}})
}

// A served device in Inventory reads nothing of the owner's INDI
// device, and the wheel's properties stay in the camera's
// status.properties.
func TestAServedDeviceInInventoryReadsNothingOfItsOwner(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		putUninstalledWheel(w)
		readyOf(w)
		wheel, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if s := wheel.Status; s.Phase != observatory.DeviceInventory || s.IndiDevice != "" || s.Endpoint != nil || s.Slot != nil {
			t.Errorf("the filter wheel in Inventory is %s on %q at %+v with slot %+v, want phase Inventory with no INDI device, endpoint, or slot", s.Phase, s.IndiDevice, s.Endpoint, s.Slot)
		}
		camera, _ := decode[observatory.Camera](t, w.api, kindCollection(observatory.CameraKind), "east-main")
		if names := propertyNames(camera.Status.Properties); !slices.Contains(names, "FILTER_SLOT") {
			t.Errorf("Camera east-main's properties = %v, want FILTER_SLOT", names)
		}
	})
}

// When a served device in Inventory and a placed one of the same kind
// name the same owner, the placed one reads the owner's INDI device,
// whatever the order of their names.
func TestAPlacedServedDeviceReadsTheOwnerWhileOneInInventoryReadsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		putUninstalledWheel(w)
		w.put(observatory.FilterWheelKind, "east-new", map[string]any{"opticalTrain": "east-imaging",
			"device": map[string]any{"kind": "Camera", "name": "east-main"}, "filters": []any{"L"}})
		readyOf(w)
		placed, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east-new")
		if placed.Status.Slot == nil {
			t.Error("the placed filter wheel has no slot, want it to read the camera's wheel")
		}
		inventoried, _ := decode[observatory.FilterWheel](t, w.api, kindCollection(observatory.FilterWheelKind), "east")
		if inventoried.Status.Slot != nil || inventoried.Status.IndiDevice != "" {
			t.Errorf("the filter wheel in Inventory has slot %+v and INDI device %q, want neither", inventoried.Status.Slot, inventoried.Status.IndiDevice)
		}
	})
}
