package observatory

import "time"

// A procedure is a list of actions that the operator runs on one
// resource when a trigger fires. Each resource states its own
// procedures, so each site composes its own routine from the actions
// that its kinds support. An action is a target state, such as a park,
// so it is safe to run twice and safe to run again after an operator
// restart.
//
// Every device kind, the Telescope, and the Observatory embed
// Procedures with the action type of their kind, so the CRD of each
// kind accepts only the actions that the kind supports. Plan 13 gives
// the design.

// Procedures are the triggers of one resource and the actions that
// each one runs.
type Procedures[A any] struct {
	// Activation runs as the resource's Telescope or Observatory turns
	// Active, while every device is connected.
	Activation []A `json:"activation,omitempty"`
	// Deactivation runs as the resource's Telescope or Observatory
	// stops being Active, while every device is still connected.
	Deactivation []A `json:"deactivation,omitempty"`
	// On lists triggers on conditions, which run while the resource is
	// active.
	On []Trigger[A] `json:"on,omitempty"`
}

// Trigger runs its actions once for each transition of a condition to
// the status that When names.
type Trigger[A any] struct {
	When When `json:"when"`
	Run  []A  `json:"run"`
}

// When is the condition of a trigger, and how long its status must
// hold.
type When struct {
	ConditionRef
	// For is a Go duration such as 20m. With none, the trigger runs at
	// the transition.
	For string `json:"for,omitempty"`
}

// Ref names one resource of the group by its kind and name, as a
// scaleTargetRef does.
type Ref struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// ConditionRef names one condition of one resource, and the status
// that it must have. With no kind, the condition is the resource's
// own. An empty status means True.
type ConditionRef struct {
	Kind   string          `json:"kind,omitempty"`
	Name   string          `json:"name,omitempty"`
	Type   string          `json:"type"`
	Status ConditionStatus `json:"status,omitempty"`
}

// ActionBase holds the fields that every action has, whatever it does.
type ActionBase struct {
	// Timeout bounds the action, its waits for Requires and After
	// included, as a Go duration such as 20m. With no timeout, the
	// action takes the default of what it does.
	Timeout string `json:"timeout,omitempty"`
	// Requires lists the conditions that must hold before the action
	// runs.
	Requires []ConditionRef `json:"requires,omitempty"`
	// After lists the resources whose runs for the same event must end
	// before the action runs.
	After []Ref `json:"after,omitempty"`
}

// Action is the action of a kind that has no action of its own.
type Action struct {
	ActionBase
}

// ParkState is the target of a Dome's or a Mount's park.
type ParkState string

const (
	StateParked   ParkState = "Parked"
	StateUnparked ParkState = "Unparked"
)

// ParkAction is the action of a Dome or a Mount.
type ParkAction struct {
	ActionBase
	State ParkState `json:"state,omitempty"`
}

// CoverState is the target of a DustCap's cover.
type CoverState string

const (
	StateOpen   CoverState = "Open"
	StateClosed CoverState = "Closed"
)

// CoverAction is the action of a DustCap.
type CoverAction struct {
	ActionBase
	State CoverState `json:"state,omitempty"`
}

// LightState is the target of a FlatPanel's light.
type LightState string

const (
	StateOn  LightState = "On"
	StateOff LightState = "Off"
)

// LightAction is the action of a FlatPanel.
type LightAction struct {
	ActionBase
	State LightState `json:"state,omitempty"`
}

// CameraAction is the action of a Camera: cool the sensor, or warm it
// and switch the cooler off.
type CameraAction struct {
	ActionBase
	Cool *Temperature `json:"cool,omitempty"`
	Warm *Temperature `json:"warm,omitempty"`
}

// Temperature is a cooler's setpoint and how close the sensor must
// come to it.
type Temperature struct {
	// Celsius is the setpoint in degrees Celsius.
	Celsius float64 `json:"celsius"`
	// Within is in degrees Celsius. Zero means the default, 0.5.
	Within float64 `json:"within,omitempty"`
}

// The default timeouts of each action, which an action's timeout
// replaces.
const (
	// A dome turns to its park position, which takes minutes.
	StateTimeout = 10 * time.Minute
	// The CCD simulator cools 0.5 °C a second, and a real sensor
	// takes minutes to settle.
	CoolTimeout = 20 * time.Minute
	// A cooler cannot warm a sensor above the air around it, so a
	// warm-up ends at its timeout wherever the sensor is.
	WarmTimeout = 10 * time.Minute
)

// DefaultWithin is how close to a setpoint a sensor must come, in
// degrees Celsius, when an action states no within.
const DefaultWithin = 0.5

// The triggers of a run's record.
const (
	TriggerActivation   = "activation"
	TriggerDeactivation = "deactivation"
)

// ConditionActive is True on a Telescope from when its reservation
// begins the Activation step until its Deactivation step begins, and
// on an Observatory from when the first reservation in it begins the
// Activation step until the last one ends. Activation runs as it turns
// True, and deactivation as it turns False.
const ConditionActive = "Active"

// ProcedureRun is the record of one trigger's last run.
type ProcedureRun struct {
	// Trigger names the trigger: activation, deactivation, or on[0]
	// and so on for each item of spec.on.
	Trigger string `json:"trigger"`
	// Since is the transition time of the condition that the run
	// answers.
	Since     *time.Time  `json:"since,omitempty"`
	State     StepState   `json:"state"`
	StartTime *time.Time  `json:"startTime,omitempty"`
	StopTime  *time.Time  `json:"stopTime,omitempty"`
	Summary   string      `json:"summary,omitempty"`
	Actions   []ActionRun `json:"actions,omitempty"`
}

// ActionRun is the record of one action of a run.
type ActionRun struct {
	// Action reads like the action's YAML, such as "state: Parked".
	Action    string     `json:"action"`
	State     StepState  `json:"state"`
	StartTime *time.Time `json:"startTime,omitempty"`
	StopTime  *time.Time `json:"stopTime,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}
