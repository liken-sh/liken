package main

// An action of a procedure, and how the operator carries it out on a
// device. Each action describes a target state. The operator reads the
// device's properties first and sends nothing when the device already
// matches (indidevice.go), so a run that an operator restart
// interrupted can run again safely.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// action has the fields of every kind's actions, so one type decodes
// the procedures of every kind. The type accepts a field that a kind
// does not have, because the CRD of each kind rejects it first.
type action struct {
	observatory.ActionBase
	State string                   `json:"state,omitempty"`
	Cool  *observatory.Temperature `json:"cool,omitempty"`
	Warm  *observatory.Temperature `json:"warm,omitempty"`
}

// procedures converts the procedures of a kind with only the common
// action fields into procedures of the action type.
func procedures(in observatory.Procedures[observatory.Action]) observatory.Procedures[action] {
	actions := func(in []observatory.Action) []action {
		var out []action
		for _, a := range in {
			out = append(out, action{ActionBase: a.ActionBase})
		}
		return out
	}
	out := observatory.Procedures[action]{Requires: in.Requires, Activation: actions(in.Activation), Deactivation: actions(in.Deactivation)}
	for _, trigger := range in.Triggers {
		out.Triggers = append(out.Triggers, observatory.Trigger[action]{When: trigger.When, Run: actions(trigger.Run)})
	}
	return out
}

// String formats the action like its YAML, such as "state: Parked" or
// "cool: -10 °C within 0.5 °C".
func (a action) String() string {
	switch {
	case a.Cool != nil:
		return "cool: " + temperatureText(*a.Cool)
	case a.Warm != nil:
		return "warm: " + temperatureText(*a.Warm)
	case a.State != "":
		return "state: " + a.State
	case a.Job != nil:
		return "job: " + a.Job.Image
	case a.Notify != "":
		return notifyPrefix + a.Notify
	}
	return "no action"
}

func temperatureText(t observatory.Temperature) string {
	return quantity(t.Celsius, 1, "°C") + " within " + quantity(within(t), 1, "°C")
}

// timeout returns how long the action may run, including its waits.
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
	case a.Job != nil:
		return observatory.JobTimeout, nil
	}
	return observatory.StateTimeout, nil
}

// skipError is returned by an action that does not apply to the device,
// such as a cool action on a camera with no cooler. The procedure
// continues with the next action.
type skipError string

func (s skipError) Error() string { return string(s) }

func skip(why string) error { return skipError(why) }

func isSkip(err error) bool {
	var s skipError
	return errors.As(err, &s)
}

// switchTarget is the INDI switch member that an action state turns On,
// with the wording for the step summary.
type switchTarget struct {
	property, member string
	// doing describes the change while it runs, did describes it after
	// the device changed, and found describes the state that the device
	// already reported.
	doing, did, found string
}

// act carries out one action on a connected device. wait has the
// action's deadline. ctx has no deadline, because the cooler's switch-off
// after a warm-up must finish even when the action's deadline has
// passed.
func (o *operator) act(ctx, wait context.Context, h handle, a action, report func(string)) (string, error) {
	of := interfaces[h.d.kind]
	switch {
	case a.Cool != nil && of.cooler:
		return cool(wait, h, *a.Cool, report)
	case a.Warm != nil && of.cooler:
		return warm(ctx, wait, h, *a.Warm, report)
	case a.State != "":
		target, ok := of.states[a.State]
		if !ok {
			return "", fmt.Errorf("a %s has no state %s", h.d.kind.Name, a.State)
		}
		if of.locks {
			// Park locks can refuse this move, so send each driver
			// the current park states first (locks.go).
			o.relayLocks(o.snapshot())
		}
		report(target.doing + " " + h.String())
		changed, err := h.switchOn(wait, target.property, target.member)
		if err != nil {
			return "", err
		}
		summary := "found " + h.String() + " " + target.found
		if changed {
			summary = target.did + " " + h.String()
		}
		if h.d.kind != observatory.DomeKind {
			return summary, nil
		}
		moved, err := shutterFollows(wait, h, a.State, report)
		if err != nil || moved == "" {
			return summary, err
		}
		return summary + " and " + moved, nil
	}
	return "", fmt.Errorf("a %s cannot run %s", h.d.kind.Name, a)
}

// shutterTargets maps a dome's park state to the shutter switch:
// Parked closes the shutter, and Unparked opens it.
var shutterTargets = map[string]switchTarget{
	"Parked":   {"DOME_SHUTTER", "SHUTTER_CLOSE", "closing the shutter of", "closed its shutter", ""},
	"Unparked": {"DOME_SHUTTER", "SHUTTER_OPEN", "opening the shutter of", "opened its shutter", ""},
}

// shutterFollows moves a dome's shutter to match its park state and
// returns what it did, or "" when the shutter was already there. The
// driver applies DOME_SHUTTER_PARK_POLICY only when the park state
// changes, so a dome that starts unparked with its shutter closed stays
// closed after an unpark. A dome with no DOME_SHUTTER property, such as
// a roll-off roof, has no shutter to move.
func shutterFollows(ctx context.Context, h handle, state string, report func(string)) (string, error) {
	target := shutterTargets[state]
	if _, ok := h.client().Property(h.name, target.property); !ok {
		return "", nil
	}
	report(target.doing + " " + h.String())
	changed, err := h.switchOn(ctx, target.property, target.member)
	if err != nil || !changed {
		return "", err
	}
	return target.did, nil
}
