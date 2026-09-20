package main

// The declared zone controls' confirmation: whether the receiver has
// reported a declared control at the declared value.

import (
	"reflect"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// A declared control the receiver has not reported is skipped, so an
// empty report confirms. There is nothing to confirm the control
// against, and blocking on it would retry forever.
func TestZoneSpecConfirmedBySkipsAnUnreportedControl(t *testing.T) {
	volume := 40.0
	spec := ZoneSpec{Power: equipment.PowerOn, Input: "CD", Volume: &volume, Sleep: &sleep}
	observed := equipment.ZoneState{Volume: equipment.Unknown, Sleep: equipment.Unknown}

	if !spec.ConfirmedBy(observed, 2) {
		t.Error("a declared control the receiver has not reported did not confirm")
	}
}

// A declared control reported at another value is not confirmed, so the
// operator sends it again.
func TestZoneSpecConfirmedByRejectsADifferentReportedValue(t *testing.T) {
	volume := 40.0
	spec := ZoneSpec{Volume: &volume}
	observed := equipment.ZoneState{Volume: 50}

	if spec.ConfirmedBy(observed, 2) {
		t.Error("a volume reported at another level was confirmed")
	}
}

// A declared control reported at the same value is confirmed. Volume
// compares in the driver's steps, so a display unit turns into a count
// first.
func TestZoneSpecConfirmedByAcceptsAnEqualReportedValue(t *testing.T) {
	volume := 40.0
	spec := ZoneSpec{Volume: &volume, Mute: &muted, Sleep: &sleep}
	observed := equipment.ZoneState{Volume: 80, Mute: true, Sleep: sleep}

	if !spec.ConfirmedBy(observed, 2) {
		t.Error("controls reported at the same values were not confirmed")
	}
}

// A different reported power is not confirmed: a declared on is not met
// by a standby report, whichever word the receiver used for standby.
func TestZoneSpecConfirmedByRejectsADifferentPower(t *testing.T) {
	spec := ZoneSpec{Power: equipment.PowerOn}
	for _, power := range []equipment.Power{equipment.PowerStandby, equipment.PowerOff} {
		if spec.ConfirmedBy(equipment.ZoneState{Power: power}, 2) {
			t.Errorf("a declared on was confirmed by a reported %s", power)
		}
	}
}

// A different reported input is not confirmed.
func TestZoneSpecConfirmedByRejectsADifferentInput(t *testing.T) {
	spec := ZoneSpec{Input: "CD"}
	if spec.ConfirmedBy(equipment.ZoneState{Input: "TV"}, 2) {
		t.Error("a declared input was confirmed by another input")
	}
}

// A different reported mute is not confirmed.
func TestZoneSpecConfirmedByRejectsADifferentMute(t *testing.T) {
	spec := ZoneSpec{Mute: &muted}
	if spec.ConfirmedBy(equipment.ZoneState{Mute: false}, 2) {
		t.Error("a declared mute was confirmed by an unmuted report")
	}
}

// A different reported sleep is not confirmed.
func TestZoneSpecConfirmedByRejectsADifferentSleep(t *testing.T) {
	spec := ZoneSpec{Sleep: &sleep}
	if spec.ConfirmedBy(equipment.ZoneState{Sleep: sleep + 30}, 2) {
		t.Error("a declared sleep was confirmed by another sleep")
	}
}

// A declared control whose observed counterpart is empty is skipped, so
// a power that has not reached the report does not fail the block.
func TestZoneSpecConfirmedBySkipsAnUnreportedPower(t *testing.T) {
	spec := ZoneSpec{Power: equipment.PowerOn}
	if !spec.ConfirmedBy(equipment.ZoneState{}, 2) {
		t.Error("a declared power the receiver has not reported did not confirm")
	}
}

// A different value in any one declared control fails the whole block,
// so a partial apply is never taken for confirmed. The sweep walks the
// zone spec and changes one field at a time, so a control added to
// ZoneSpec without a line in ConfirmedBy fails here instead of being
// taken for confirmed while the receiver holds another value. Each
// field pairs with the ZoneState field of the same name.
func TestZoneSpecConfirmedByRejectsADifferenceInAnyDeclaredField(t *testing.T) {
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

// declareZoneField gives one ZoneSpec field a declared value. A field of
// a type the sweep cannot fill fails, so a new control type is handled
// here rather than skipped.
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

// changeZoneState moves one observed control off the declared value. The
// volume and the sleep are compared as counts, and the declared 1 is one
// step at resolution 1, so the 2 set here differs.
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
