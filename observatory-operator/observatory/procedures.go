package observatory

import "time"

// A procedure is a list of actions that the operator runs on one
// resource when a trigger fires. An action sets a target state, such as
// parked, so running it twice, or again after the operator restarts,
// does no harm.
//
// Each device kind, the Telescope, and the Observatory embed
// Procedures with their own action type, so the CRD of each kind
// accepts only the actions that the kind supports.

// Procedures holds the triggers of one resource and the actions that
// each one runs.
type Procedures[A any] struct {
	// Requires lists the conditions that must be true before a
	// reservation's session starts the resource. The session checks the
	// requirements of the Observatory and of its devices in the Wait
	// step, before StartSite. It checks the requirements of the
	// Telescope and of its devices in the Requires step, before
	// PowerOn. A requirement is checked only at the start. When the
	// condition turns False later, only a trigger can act on it.
	Requires []ConditionRef `json:"requires,omitempty"`
	// Activation runs when the resource's Telescope or Observatory
	// turns Active, while every device is connected.
	Activation []A `json:"activation,omitempty"`
	// Deactivation runs when the resource's Telescope or Observatory
	// stops being Active, while every device is still connected.
	Deactivation []A `json:"deactivation,omitempty"`
	// Triggers run actions when a condition changes, while the resource
	// is active. The field is not called on, because YAML 1.1, which
	// kubectl parses through sigs.k8s.io/yaml, reads a bare on key as
	// the boolean true.
	Triggers []Trigger[A] `json:"triggers,omitempty"`
}

// Trigger runs Run once each time the condition in When changes to
// the status in When.
type Trigger[A any] struct {
	When When `json:"when"`
	Run  []A  `json:"run"`
}

// When is the condition that fires a trigger, and how long the
// condition must keep that status first.
type When struct {
	ConditionRef
	// For is a Go duration such as 20m. When it is empty, the trigger
	// runs at the transition.
	For string `json:"for,omitempty"`
}

// Ref refers to one resource of the API group by its kind and name,
// as a scaleTargetRef does.
type Ref struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// ConditionRef refers to one condition of one resource and the status
// that it must have. When Kind is empty, the condition belongs to the
// resource that holds the reference. An empty Status means True.
type ConditionRef struct {
	Kind   string          `json:"kind,omitempty"`
	Name   string          `json:"name,omitempty"`
	Type   string          `json:"type"`
	Status ConditionStatus `json:"status,omitempty"`
}

// ActionBase holds the fields that the action of every kind has: a
// job, which any resource can run, and the timeout, requirements, and
// ordering of the action.
type ActionBase struct {
	// Job runs a container as a Kubernetes Job, for work that no other
	// action does.
	Job *Job `json:"job,omitempty"`
	// Notify asks a person to do something that no driver can, such as
	// covering a telescope. The resource's AttentionNeeded condition
	// turns True with this text as its message. The action does not
	// wait for the person.
	Notify string `json:"notify,omitempty"`
	// Timeout bounds the action, including its waits for Requires and
	// After, as a Go duration such as 20m. When it is empty, the action
	// uses the default timeout for its kind of work (StateTimeout and
	// the constants after it).
	Timeout string `json:"timeout,omitempty"`
	// Requires lists the conditions that must be true before the
	// action runs.
	Requires []ConditionRef `json:"requires,omitempty"`
	// After lists the resources whose runs for the same event must
	// finish before the action runs.
	After []Ref `json:"after,omitempty"`
}

// Job is a container that the operator runs once as a batch/v1 Job in
// its own namespace, such as a script that switches a dew heater's
// relay or posts to a webhook.
type Job struct {
	// Image is the container image, used as written.
	Image   string   `json:"image"`
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Env adds variables to the container's environment. The operator
	// adds its own variables after them (EnvObservatory and the
	// constants after it).
	Env []EnvVar `json:"env,omitempty"`
}

// EnvVar is one variable of a Job's environment.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// The variables that the operator adds to a Job's environment, so a
// script can tell what ran it and where the resource's INDI server
// listens.
const (
	EnvObservatory = "LIKEN_OBSERVATORY"
	EnvTelescope   = "LIKEN_TELESCOPE"
	EnvResource    = "LIKEN_RESOURCE"
	EnvTrigger     = "LIKEN_TRIGGER"
	EnvINDIHost    = "INDI_HOST"
	EnvINDIPort    = "INDI_PORT"
)

// Action is the action type of a kind with no target state of its
// own, so its actions can only run a job or notify a person.
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

// LightState is the target of a FlatPanel's light. The states are not
// named On and Off because YAML 1.1 reads a bare On or Off as a
// boolean. StateLit matches the panel's Lit condition.
type LightState string

const (
	StateLit  LightState = "Lit"
	StateDark LightState = "Dark"
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
	// Within is in degrees Celsius. Zero means DefaultWithin.
	Within float64 `json:"within,omitempty"`
}

// The default timeouts of the actions. An action's Timeout field
// replaces them.
const (
	// A dome takes minutes to turn to its park position.
	StateTimeout = 10 * time.Minute
	// The INDI CCD simulator cools 0.5 °C a second, and a real sensor
	// takes minutes to settle.
	CoolTimeout = 20 * time.Minute
	// A cooler cannot warm a sensor above the air around it, so on a
	// cold night a warm-up can run until this timeout.
	WarmTimeout = 10 * time.Minute
	// A job's script, such as a webhook or a relay, takes seconds, and
	// the Job's pod may first have to pull its image.
	JobTimeout = 10 * time.Minute
)

// DefaultWithin is how close to a setpoint a sensor must come, in
// degrees Celsius, when an action sets no within.
const DefaultWithin = 0.5

// The values of ProcedureRun.Trigger for the activation and
// deactivation runs.
const (
	TriggerActivation   = "activation"
	TriggerDeactivation = "deactivation"
)

// ConditionActive is True on a Telescope from when its reservation
// begins the Activation step until its Deactivation step begins. On an
// Observatory it is True from when the first reservation in it begins
// the Activation step until the last reservation ends. Activation runs
// when the condition turns True, and deactivation when it turns False.
const ConditionActive = "Active"

// ProcedureRun records the last run of one trigger.
type ProcedureRun struct {
	// Trigger is activation, deactivation, or triggers[i] for item i of
	// spec.triggers.
	Trigger string `json:"trigger"`
	// Since is the transition time of the condition that caused the
	// run.
	Since     *time.Time  `json:"since,omitempty"`
	State     StepState   `json:"state"`
	StartTime *time.Time  `json:"startTime,omitempty"`
	StopTime  *time.Time  `json:"stopTime,omitempty"`
	Summary   string      `json:"summary,omitempty"`
	Actions   []ActionRun `json:"actions,omitempty"`
}

// ActionRun records one action of a run.
type ActionRun struct {
	// Action is the action written as in its YAML, such as
	// "state: Parked".
	Action    string     `json:"action"`
	State     StepState  `json:"state"`
	StartTime *time.Time `json:"startTime,omitempty"`
	StopTime  *time.Time `json:"stopTime,omitempty"`
	Summary   string     `json:"summary,omitempty"`
}
