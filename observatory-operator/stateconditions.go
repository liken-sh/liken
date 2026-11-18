package main

// The conditions that report a device's state: Parked, Open, Lit,
// Cooling, and Safe. Procedure triggers read them, and several printer
// columns show their reasons. Each one is computed from a standard INDI
// property that the status also shows as a typed field
// (devicesplit.go). The status writer sets them only while the device
// is connected, because a disconnected driver deletes its properties,
// and a condition kept from before could report a stale state.
//
// INDI sets a park, shutter, or cover property to Busy while the device
// moves, so those conditions are Unknown with the reason Moving until
// the move ends. A cooler and a light read only their switch, because
// drivers report Busy for the whole time that cooling runs, and Busy
// there does not mean a move.

import (
	"fmt"

	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	reasonMoving      = "Moving"
	reasonNotReported = "NotReported"
)

// stateRule reads one condition from one switch property.
type stateRule struct {
	condition, property string
	// on and off are the switch members that make the condition True
	// and False.
	on, off             string
	onReason, offReason string
	// subject is a format string for the start of the message. Its
	// argument is the device's kind and name, such as "Dome lab". The
	// message continues with onText, offText, or "is moving".
	subject         string
	onText, offText string
	// moves is true when a Busy property means that the device is
	// moving, so the state is Unknown until the move ends.
	moves bool
}

var stateRules = map[observatory.Kind][]stateRule{
	observatory.DomeKind: {
		{observatory.ConditionParked, "DOME_PARK", "PARK", "UNPARK", "Parked", "Unparked", "%s", "is parked", "is unparked", true},
		{observatory.ConditionOpen, "DOME_SHUTTER", "SHUTTER_OPEN", "SHUTTER_CLOSE", "Open", "Closed", "The shutter of %s", "is open", "is closed", true},
	},
	observatory.MountKind: {
		{observatory.ConditionParked, "TELESCOPE_PARK", "PARK", "UNPARK", "Parked", "Unparked", "%s", "is parked", "is unparked", true},
	},
	observatory.DustCapKind: {
		{observatory.ConditionOpen, "CAP_PARK", "UNPARK", "PARK", "Open", "Closed", "%s", "is open", "is closed", true},
	},
	observatory.FlatPanelKind: {
		{observatory.ConditionLit, "FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON", "FLAT_LIGHT_OFF", "LightOn", "LightOff", "The light of %s", "is on", "is off", false},
	},
	observatory.CameraKind: {
		{observatory.ConditionCooling, "CCD_COOLER", "COOLER_ON", "COOLER_OFF", "CoolerOn", "CoolerOff", "The cooler of %s", "is on", "is off", false},
	},
}

// stateConditions returns the state conditions of a connected device.
// A property that the driver does not define produces no condition.
func stateConditions(d *device, r reader) []observatory.Condition {
	label := d.kind.Name + " " + d.name()
	var out []observatory.Condition
	for _, rule := range stateRules[d.kind] {
		if p, ok := r.property(rule.property); ok {
			out = append(out, rule.read(p, label))
		}
	}
	if p, ok := r.property("SAFETY_STATUS"); ok && d.kind == observatory.WeatherStationKind {
		out = append(out, safeCondition(p, label))
	}
	return out
}

func (s stateRule) read(p indi.Property, label string) observatory.Condition {
	subject := fmt.Sprintf(s.subject, label)
	switch {
	case s.moves && p.State == indi.Busy:
		return condition(s.condition, observatory.ConditionUnknown, reasonMoving, subject+" is moving")
	case isOn(p, s.on):
		return condition(s.condition, observatory.ConditionTrue, s.onReason, subject+" "+s.onText)
	case isOn(p, s.off):
		return condition(s.condition, observatory.ConditionFalse, s.offReason, subject+" "+s.offText)
	}
	return condition(s.condition, observatory.ConditionUnknown, reasonNotReported,
		fmt.Sprintf("%s reports neither %s nor %s On in %s", label, s.on, s.off, s.property))
}

// safeCondition turns the Safety that stationSafety reads from a
// weather station into the Safe condition. Warning and Danger both make
// Safe False, so a trigger on Safe False acts on either, and the reason
// says which one the station reports.
func safeCondition(p indi.Property, label string) observatory.Condition {
	level := stationSafety(p)
	message := fmt.Sprintf("%s reports %s", label, level)
	switch level {
	case observatory.SafetySafe:
		return condition(observatory.ConditionSafe, observatory.ConditionTrue, string(level), message)
	case observatory.SafetyWarning, observatory.SafetyDanger:
		return condition(observatory.ConditionSafe, observatory.ConditionFalse, string(level), message)
	}
	return condition(observatory.ConditionSafe, observatory.ConditionUnknown, reasonNotReported, label+" reports no safety level")
}
