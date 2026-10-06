package main

// An action of a procedure, and what the operator sends a device to
// carry it out. Each action is a target state, so the operator first
// reads what the device reports, and sends nothing when the device is
// there already (indidevice.go). That makes a run that an operator
// restart interrupted safe to run again.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// action holds every field that an action of any kind has, so one type
// reads the procedures of every kind. The CRD of each kind refuses the
// fields that its kind does not have, as deviceSpec does for the spec.
type action struct {
	observatory.ActionBase
	State string                   `json:"state,omitempty"`
	Cool  *observatory.Temperature `json:"cool,omitempty"`
	Warm  *observatory.Temperature `json:"warm,omitempty"`
}

// procedures answers the procedures of a kind that has no action of
// its own, with the union type.
func procedures(in observatory.Procedures[observatory.Action]) observatory.Procedures[action] {
	actions := func(in []observatory.Action) []action {
		var out []action
		for _, a := range in {
			out = append(out, action{ActionBase: a.ActionBase})
		}
		return out
	}
	out := observatory.Procedures[action]{Activation: actions(in.Activation), Deactivation: actions(in.Deactivation)}
	for _, trigger := range in.Triggers {
		out.Triggers = append(out.Triggers, observatory.Trigger[action]{When: trigger.When, Run: actions(trigger.Run)})
	}
	return out
}

// String reads like the action's YAML, such as "state: Parked" or
// "cool: -10 °C within 0.5 °C".
func (a action) String() string {
	switch {
	case a.Cool != nil:
		return "cool: " + temperatureText(*a.Cool)
	case a.Warm != nil:
		return "warm: " + temperatureText(*a.Warm)
	case a.State != "":
		return "state: " + a.State
	}
	return "no action"
}

func temperatureText(t observatory.Temperature) string {
	return quantity(t.Celsius, 1, "°C") + " within " + quantity(within(t), 1, "°C")
}

// timeout answers how long the action may take, its waits included.
func (a action) timeout() (time.Duration, error) {
	if a.Timeout != "" {
		d, err := time.ParseDuration(a.Timeout)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("timeout: %q is not a positive duration", a.Timeout)
		}
		return d, nil
	}
	switch {
	case a.Cool != nil:
		return observatory.CoolTimeout, nil
	case a.Warm != nil:
		return observatory.WarmTimeout, nil
	}
	return observatory.StateTimeout, nil
}

// skipError is the outcome of an action that had nothing it could do,
// such as a cool action on a camera with no cooler. Its run goes on.
type skipError string

func (s skipError) Error() string { return string(s) }

func skip(why string) error { return skipError(why) }

func isSkip(err error) bool {
	var s skipError
	return errors.As(err, &s)
}

// switchTarget is the switch that one state of one kind turns On, with
// the words that a summary uses.
type switchTarget struct {
	property, member string
	// doing names the change while it runs, did once it is done, and
	// found the state that the device reported already.
	doing, did, found string
}

// switchTargets maps each kind and state to the switch that reaches it.
var switchTargets = map[string]switchTarget{
	"Dome/Parked":    {"DOME_PARK", "PARK", "parking", "parked", "parked"},
	"Dome/Unparked":  {"DOME_PARK", "UNPARK", "unparking", "unparked", "unparked"},
	"Mount/Parked":   {"TELESCOPE_PARK", "PARK", "parking", "parked", "parked"},
	"Mount/Unparked": {"TELESCOPE_PARK", "UNPARK", "unparking", "unparked", "unparked"},
	"DustCap/Open":   {"CAP_PARK", "UNPARK", "opening", "opened", "open"},
	"DustCap/Closed": {"CAP_PARK", "PARK", "closing", "closed", "closed"},
	"FlatPanel/Lit":  {"FLAT_LIGHT_CONTROL", "FLAT_LIGHT_ON", "switching on the light of", "switched on the light of", "lit"},
	"FlatPanel/Dark": {"FLAT_LIGHT_CONTROL", "FLAT_LIGHT_OFF", "switching off the light of", "switched off the light of", "dark"},
}

// act carries out one action on a connected device. wait holds the
// action's deadline, and ctx outlives it, for the one action that must
// finish after its deadline: the cooler's switch-off after a warm-up.
func (o *operator) act(ctx, wait context.Context, h handle, a action, report func(string)) (string, error) {
	switch {
	case a.Cool != nil && h.d.kind == observatory.CameraKind:
		return cool(wait, h, *a.Cool, report)
	case a.Warm != nil && h.d.kind == observatory.CameraKind:
		return warm(ctx, wait, h, *a.Warm, report)
	case a.State != "":
		target, ok := switchTargets[h.d.kind.Name+"/"+a.State]
		if !ok {
			return "", fmt.Errorf("a %s has no state %s", h.d.kind.Name, a.State)
		}
		if h.d.kind == observatory.DomeKind || h.d.kind == observatory.MountKind {
			// The park locks decide this move, so each driver holds
			// the current park states before the move arrives
			// (locks.go).
			o.relayLocks(o.snapshot())
		}
		report(target.doing + " " + h.String())
		changed, err := h.switchOn(wait, target.property, target.member)
		if err != nil {
			return "", err
		}
		if changed {
			return target.did + " " + h.String(), nil
		}
		return "found " + h.String() + " " + target.found, nil
	}
	return "", fmt.Errorf("a %s cannot run %s", h.d.kind.Name, a)
}
