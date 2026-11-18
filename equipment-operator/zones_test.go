package main

import (
	"reflect"
	"testing"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// A declared control that the receiver has not reported is skipped, so
// an empty report confirms, because waiting for it would retry forever.
func TestZoneSpecConfirmedBySkipsAnUnreportedControl(t *testing.T) {
	volume := 40.0
	spec := ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &volume, Sleep: &sleep}
	observed := equipment.ZoneState{Volume: equipment.Unknown, Sleep: equipment.Unknown}

	if !spec.ConfirmedBy(observed, 2) {
		t.Error("a declared control the receiver has not reported did not confirm")
	}
}

// A volume that the receiver reports at another value is not confirmed,
// so the operator sends it again.
func TestZoneSpecConfirmedByRejectsADifferentReportedValue(t *testing.T) {
	t.Parallel()
	volume := 40.0
	spec := ZoneSpec{Volume: &volume}
	observed := equipment.ZoneState{Volume: 50}

	if spec.ConfirmedBy(observed, 2) {
		t.Error("a volume reported at another level was confirmed")
	}
}

// Controls that the receiver reports at their declared values confirm.
// Volume compares in the driver's steps, so the declared 40 in the
// receiver's scale is 80 steps at resolution 2.
func TestZoneSpecConfirmedByAcceptsAnEqualReportedValue(t *testing.T) {
	volume := 40.0
	spec := ZoneSpec{Volume: &volume, Mute: &muted, Sleep: &sleep}
	observed := equipment.ZoneState{Volume: 80, Mute: true, Sleep: sleep}

	if !spec.ConfirmedBy(observed, 2) {
		t.Error("controls reported at the same values were not confirmed")
	}
}

// A declared on is not confirmed by a report of standby, whichever word
// the receiver uses for standby.
func TestZoneSpecConfirmedByRejectsADifferentPower(t *testing.T) {
	t.Parallel()
	spec := ZoneSpec{Power: equipment.PowerOn}
	for _, power := range []equipment.Power{equipment.PowerStandby, equipment.PowerOff} {
		if spec.ConfirmedBy(equipment.ZoneState{Power: power}, 2) {
			t.Errorf("a declared on was confirmed by a reported %s", power)
		}
	}
}

func TestZoneSpecConfirmedByRejectsADifferentInput(t *testing.T) {
	t.Parallel()
	spec := ZoneSpec{Input: "CD"}
	if spec.ConfirmedBy(equipment.ZoneState{Input: "TV"}, 2) {
		t.Error("a declared input was confirmed by another input")
	}
}

func TestZoneSpecConfirmedByRejectsADifferentMute(t *testing.T) {
	spec := ZoneSpec{Mute: &muted}
	if spec.ConfirmedBy(equipment.ZoneState{Mute: false}, 2) {
		t.Error("a declared mute was confirmed by an unmuted report")
	}
}

func TestZoneSpecConfirmedByRejectsADifferentSleep(t *testing.T) {
	spec := ZoneSpec{Sleep: &sleep}
	if spec.ConfirmedBy(equipment.ZoneState{Sleep: sleep + 30}, 2) {
		t.Error("a declared sleep was confirmed by another sleep")
	}
}

// A power that is missing from the report is skipped and does not fail
// the zone.
func TestZoneSpecConfirmedBySkipsAnUnreportedPower(t *testing.T) {
	t.Parallel()
	spec := ZoneSpec{Power: equipment.PowerOn}
	if !spec.ConfirmedBy(equipment.ZoneState{}, 2) {
		t.Error("a declared power the receiver has not reported did not confirm")
	}
}

// A difference in any one declared control fails the zone, so a partly
// applied spec is never confirmed. The test declares each field of
// ZoneSpec in turn and changes the ZoneState field of the same name, so
// a control added to ZoneSpec without a check in ConfirmedBy fails here.
func TestZoneSpecConfirmedByRejectsADifferenceInAnyDeclaredField(t *testing.T) {
	t.Parallel()
	specType := reflect.TypeOf(ZoneSpec{})
	stateType := reflect.TypeOf(equipment.ZoneState{})
	for i := 0; i < specType.NumField(); i++ {
		name := specType.Field(i).Name
		if _, held := stateType.FieldByName(name); !held {
			t.Fatalf("ZoneState has no field named %s", name)
		}
		spec := ZoneSpec{}
		declareZoneField(t, reflect.ValueOf(&spec).Elem().Field(i))
		observed := equipment.ZoneState{}
		changeZoneState(t, reflect.ValueOf(&observed).Elem().FieldByName(name))
		if spec.ConfirmedBy(observed, 1) {
			t.Errorf("a difference in ZoneSpec.%s was confirmed", name)
		}
	}
}

// declareZoneField sets one ZoneSpec field to a value. A field of a type
// that it cannot set fails the test, so that a new type of control is
// not skipped.
func declareZoneField(t *testing.T, f reflect.Value) {
	t.Helper()
	switch f.Kind() {
	case reflect.String:
		f.SetString("declared")
	case reflect.Ptr:
		elem := reflect.New(f.Type().Elem())
		switch elem.Elem().Kind() {
		case reflect.Float64:
			elem.Elem().SetFloat(1)
		case reflect.Int:
			elem.Elem().SetInt(1)
		case reflect.Bool:
			elem.Elem().SetBool(true)
		default:
			t.Fatalf("no sample for a *%s field", elem.Elem().Kind())
		}
		f.Set(elem)
	default:
		t.Fatalf("no sample for a %s field", f.Kind())
	}
}

// changeZoneState sets one ZoneState field to a value that differs from
// the one declareZoneField declares. At resolution 1 a declared volume
// of 1 is one step, and sleep compares in minutes, so 2 differs from
// both.
func changeZoneState(t *testing.T, f reflect.Value) {
	t.Helper()
	switch f.Kind() {
	case reflect.String:
		f.SetString("changed")
	case reflect.Int:
		f.SetInt(2)
	case reflect.Bool:
		f.SetBool(false)
	default:
		t.Fatalf("no change for a %s field", f.Kind())
	}
}

var (
	sleep = 30
	muted = true
)
