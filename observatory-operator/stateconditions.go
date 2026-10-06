package main

// The conditions of a device's state, which the triggers of procedures
// read (plan 13). Each one comes from a property that the driver
// already reports for the device's readings. The status writer adds
// them only while the device is connected: a disconnected driver
// deletes the properties, and a condition kept from before would
// describe a state that nobody can confirm.
//
// A park, a shutter, or a cover that moves reports Busy, and its
// condition is Unknown with the reason Moving until the move ends.
// Tracking, a cooler, and a light read the switch alone, because
// INDI's drivers report Busy for the whole time that tracking or
// cooling runs.

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
	// on and off are the members that make the condition True and
	// False.
	on, off             string
	onReason, offReason string
	// subject is a format of what the message describes, with the
	// device's kind and name, such as "Dome lab", as its argument. The
	// message adds onText, offText, or "is moving".
	subject         string
	onText, offText string
	// moves is true when Busy means that the device moves, and the
	// state is not known until the move ends.
	moves bool
}

var stateRules = map[observatory.Kind][]stateRule{
	observatory.DomeKind: {
		{observatory.ConditionParked, "DOME_PARK", "PARK", "UNPARK", "Parked", "Unparked", "%s", "is parked", "is unparked", true},
		{observatory.ConditionOpen, "DOME_SHUTTER", "SHUTTER_OPEN", "SHUTTER_CLOSE", "Open", "Closed", "The shutter of %s", "is open", "is closed", true},
	},
	observatory.MountKind: {
		{observatory.ConditionParked, "TELESCOPE_PARK", "PARK", "UNPARK", "Parked", "Unparked", "%s", "is parked", "is unparked", true},
		{observatory.ConditionTracking, "TELESCOPE_TRACK_STATE", "TRACK_ON", "TRACK_OFF", "Tracking", "NotTracking", "%s", "is tracking", "is not tracking", false},
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

// stateConditions answers the state conditions of a connected device.
// A property that the driver does not define gives no condition.
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

// safeCondition reads a weather station's verdict (stationSafety). A
// Warning is False as well as a Danger, so a trigger on Safe False
// acts on both, and the reason names which one the station reports.
func safeCondition(p indi.Property, label string) observatory.Condition {
	verdict := stationSafety(p)
	message := fmt.Sprintf("%s reports %s", label, verdict)
	switch verdict {
	case observatory.SafetySafe:
		return condition(observatory.ConditionSafe, observatory.ConditionTrue, string(verdict), message)
	case observatory.SafetyWarning, observatory.SafetyDanger:
		return condition(observatory.ConditionSafe, observatory.ConditionFalse, string(verdict), message)
	}
	return condition(observatory.ConditionSafe, observatory.ConditionUnknown, reasonNotReported, label+" reports no safety verdict")
}
