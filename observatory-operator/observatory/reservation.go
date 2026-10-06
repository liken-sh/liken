package observatory

import "time"

// A Reservation gives one holder the use of one Telescope. Device
// resources are inventory, and the operator starts a telescope's pods
// only while a Reservation for it is active. Activation and
// deactivation run as a fixed list of steps, and the status lists
// every step with its state, so `kubectl describe` shows how far each
// one got.

type Reservation = Object[ReservationSpec, ReservationStatus]

type ReservationSpec struct {
	Telescope string `json:"telescope"`
	// Holder names who uses the telescope: a person's desktop client,
	// or a Session of astrophotography-operator.
	Holder string `json:"holder"`
	// Start is when activation begins. With no start, it begins when
	// the Reservation is created.
	Start *time.Time `json:"start,omitempty"`
	// End is when deactivation begins. With no end, the Reservation
	// lasts until it is deleted.
	End *time.Time `json:"end,omitempty"`
}

// ReservationPhase is a reservation's state in one word.
type ReservationPhase string

const (
	// ReservationScheduled: waiting for spec.start.
	ReservationScheduled ReservationPhase = "Scheduled"
	// ReservationActivating: the activation steps run.
	ReservationActivating ReservationPhase = "Activating"
	// ReservationReady: the telescope is ready for the holder, and
	// status.endpoint is the server to connect to.
	ReservationReady ReservationPhase = "Ready"
	// ReservationDeactivating: the deactivation steps run, after
	// spec.end or a delete.
	ReservationDeactivating ReservationPhase = "Deactivating"
	// ReservationReleased: the deactivation steps are done, and the
	// devices are safe to power off.
	ReservationReleased ReservationPhase = "Released"
	// ReservationFailed: a step failed. The Ready condition names the
	// step, and the step's message names the device.
	ReservationFailed ReservationPhase = "Failed"
)

// StepName names one step of activation or deactivation. Each name is
// unique across both lists, so a reader can select one step by name,
// as in `{.status.steps[?(@.name=="Connect")].state}`.
type StepName string

const (
	StepWait         StepName = "Wait"
	StepStartSite    StepName = "StartSite"
	StepPowerOn      StepName = "PowerOn"
	StepStartDevices StepName = "StartDevices"
	StepConnect      StepName = "Connect"
	StepConfigure    StepName = "Configure"
	StepPrepare      StepName = "Prepare"

	StepAbort       StepName = "Abort"
	StepSecure      StepName = "Secure"
	StepDisconnect  StepName = "Disconnect"
	StepStopDevices StepName = "StopDevices"
	StepPowerOff    StepName = "PowerOff"
	StepStopSite    StepName = "StopSite"
)

// ActivationSteps run in this order. Plan 07 states what each one
// does.
var ActivationSteps = []StepName{
	StepWait, StepStartSite, StepPowerOn, StepStartDevices,
	StepConnect, StepConfigure, StepPrepare,
}

// DeactivationSteps run in this order, after spec.end or a delete.
var DeactivationSteps = []StepName{
	StepAbort, StepSecure, StepDisconnect, StepStopDevices,
	StepPowerOff, StepStopSite,
}

// StepState is one step's progress. A step starts only when the step
// before it is Done or Skipped.
type StepState string

const (
	StepPending StepState = "Pending"
	StepRunning StepState = "Running"
	StepDone    StepState = "Done"
	StepFailed  StepState = "Failed"
	StepSkipped StepState = "Skipped"
)

type ReservationStatus struct {
	ObservedGeneration int64            `json:"observedGeneration,omitempty"`
	Phase              ReservationPhase `json:"phase,omitempty"`
	// Step is the step that runs now, or the step that failed. It is
	// empty while the phase is Ready or Released, so the printer
	// column never shows a finished step as if it ran.
	Step StepName `json:"step,omitempty"`
	// Steps lists the activation steps from the start, and the
	// deactivation steps from when deactivation begins.
	Steps []Step `json:"steps,omitempty"`
	// Endpoint is the telescope's INDI server, while the phase is
	// Ready.
	Endpoint   *Endpoint   `json:"endpoint,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Step is one step's record. An operator that restarts resumes from
// this record and from what it observes, not from its memory.
type Step struct {
	Name      StepName   `json:"name"`
	State     StepState  `json:"state"`
	StartTime *time.Time `json:"startTime,omitempty"`
	StopTime  *time.Time `json:"stopTime,omitempty"`
	// Summary says what the step waits for or did, and names the
	// device when one device holds the step up. The JSON names of a
	// step's fields sort as name, startTime, state, stopTime, summary,
	// and `kubectl describe` prints a map's fields in that sorted
	// order, so each step begins with its name.
	Summary string `json:"summary,omitempty"`
}

const (
	// ConditionSafeToPowerOff is True when the deactivation steps are
	// done: the mount parked, the cameras warmed, the dust caps closed,
	// and the pods stopped.
	ConditionSafeToPowerOff = "SafeToPowerOff"

	// ReservationFinalizer holds a deleted Reservation until its
	// deactivation steps are done.
	ReservationFinalizer = Group + "/deactivate"
)
