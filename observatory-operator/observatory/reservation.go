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
	// or astrophotography-operator.
	Holder string `json:"holder"`
	// Start is when activation begins. With no start, it begins when
	// the Reservation is created.
	Start *time.Time `json:"start,omitempty"`
	// End is when deactivation begins. With no end, the Reservation
	// lasts until it is deleted.
	End *time.Time `json:"end,omitempty"`
	// Outbox names the volume that the telescope's imaging cameras
	// write their frames to. With no outbox, the operator leaves the
	// upload settings alone, and a driver that a client sets to keep
	// frames writes them to the pod's /tmp, which the pod loses when it
	// stops.
	Outbox *Outbox `json:"outbox,omitempty"`
	// FITSKeywords are the keywords of every frame of the session, the
	// last and most specific level of MergeFITSKeywords.
	FITSKeywords []FITSKeyword `json:"fitsKeywords,omitempty"`
}

// Outbox names a PersistentVolumeClaim in the reservation's namespace,
// in the shape of a pod's persistentVolumeClaim volume, so a later
// field such as a subPath has a place. The claim is the person's, not
// a template, so the frames stay from night to night in a place they
// chose.
type Outbox struct {
	ClaimName string `json:"claimName"`
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
	StepRequires     StepName = "Requires"
	StepPowerOn      StepName = "PowerOn"
	StepStartDevices StepName = "StartDevices"
	StepConnect      StepName = "Connect"
	StepConfigure    StepName = "Configure"
	StepActivation   StepName = "Activation"
	StepStartGuider  StepName = "StartGuider"

	StepStopWork     StepName = "StopWork"
	StepDeactivation StepName = "Deactivation"
	StepStopGuider   StepName = "StopGuider"
	StepDisconnect   StepName = "Disconnect"
	StepStopDevices  StepName = "StopDevices"
	StepPowerOff     StepName = "PowerOff"
	StepStopSite     StepName = "StopSite"
)

// ActivationSteps run in this order. Plan 07 states what each one
// does, plan 13 states the Activation step, which runs the procedures
// of the resources' activation, and plan 21 states the Requires step,
// which waits for the spec.requires of the Telescope and its devices.
var ActivationSteps = []StepName{
	StepWait, StepStartSite, StepRequires, StepPowerOn, StepStartDevices,
	StepConnect, StepConfigure, StepActivation, StepStartGuider,
}

// DeactivationSteps run in this order, after spec.end or a delete.
var DeactivationSteps = []StepName{
	StepStopWork, StepDeactivation, StepStopGuider, StepDisconnect, StepStopDevices,
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
	Endpoint *Endpoint `json:"endpoint,omitempty"`
	// Session records what the reservation's session started with, so
	// a spec edit or an eviction during the session changes nothing
	// that runs.
	Session    *Session    `json:"session,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Session is what a reservation's session started with. The StartSite
// step writes Observatory and Site, and the PowerOn step writes the
// rest, each once, before it creates a pod. A retry of a failed
// activation step writes the parts that are written again, from the
// specs as they are then. The record lives in the reservation and not
// on the pods, so an eviction that deletes every pod of a server
// leaves it.
type Session struct {
	// Observatory is the observatory of the reservation's telescope.
	// The StartSite step writes it with Site, so a record with an
	// observatory and no Site records an observatory with no devices.
	Observatory string `json:"observatory,omitempty"`
	// Site holds each device of the observatory's server, by its key,
	// such as Dome/lab. Reservations of several telescopes in one
	// observatory share its server, and each one records the devices
	// that the server ran when its StartSite step began.
	Site map[string]SessionDevice `json:"site,omitempty"`
	// Devices holds each device of the telescope's server, by its key,
	// such as Camera/east-main.
	Devices map[string]SessionDevice `json:"devices,omitempty"`
	// Trains, Tubes, and Guiders hold the spec of each OpticalTrain and
	// Guider of the telescope, and of each OpticalTube of those trains,
	// by name.
	Trains  map[string]SessionTrain  `json:"trains,omitempty"`
	Tubes   map[string]SessionTube   `json:"tubes,omitempty"`
	Guiders map[string]SessionGuider `json:"guiders,omitempty"`
	// Outbox is the reservation's spec.outbox. A claim cannot change
	// under a running driver without a new pod, and a new pod in the
	// middle of an exposure loses the exposure, so an edit waits for
	// the next session.
	Outbox *Outbox `json:"outbox,omitempty"`
	// FITSKeywords holds the merged keywords of each imaging camera, by
	// the camera's key, such as Camera/east-main, for each camera
	// whose merged list is not empty. The Configure step writes them,
	// and the driver puts each keyword it receives on every later frame
	// until it exits. Recording them keeps the declared keywords of
	// every frame of one session the same, whatever a person edits
	// during it.
	FITSKeywords map[string][]FITSKeyword `json:"fitsKeywords,omitempty"`
}

// SessionTrain, SessionTube, and SessionGuider are the spec of one
// OpticalTrain, OpticalTube, or Guider as its session started it. UID
// tells the object from one that a person created again with the same
// name during the session, which the session did not start.
type SessionTrain struct {
	UID string `json:"uid,omitempty"`
	OpticalTrainSpec
}

type SessionTube struct {
	UID string `json:"uid,omitempty"`
	OpticalTubeSpec
}

type SessionGuider struct {
	UID string `json:"uid,omitempty"`
	GuiderSpec
}

// SessionDevice is the spec of one device as its session started it.
// UID tells the device from one that a person created again with the
// same name during the session, which the session did not start.
type SessionDevice struct {
	UID          string `json:"uid,omitempty"`
	Telescope    string `json:"telescope,omitempty"`
	OpticalTrain string `json:"opticalTrain,omitempty"`
	Observatory  string `json:"observatory,omitempty"`
	DeviceSpec
	// Guides is true for a camera that a Guider of the telescope
	// guides with.
	Guides bool `json:"guides,omitempty"`
}

// Step is one step's record. An operator that restarts resumes from
// this record and from what it observes, not from its memory.
type Step struct {
	Name      StepName   `json:"name"`
	State     StepState  `json:"state"`
	StartTime *time.Time `json:"startTime,omitempty"`
	StopTime  *time.Time `json:"stopTime,omitempty"`
	// Summary says what the step waits for or did, and names the
	// device when one device holds the step up. `kubectl describe`
	// prints a map's fields in the sorted order of their JSON names:
	// actions, name, startTime, state, stopTime, summary. So a step
	// begins with its name, except a step with actions, which begins
	// with them.
	Summary string `json:"summary,omitempty"`
	// Actions copies the actions of the procedures that the
	// Activation or Deactivation step waited on, each with the
	// resource that it belongs to.
	Actions []StepAction `json:"actions,omitempty"`
}

// StepAction is one action of a procedure that a step waited on.
type StepAction struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ActionRun
}

const (
	// ConditionSafeToPowerOff is True when the deactivation steps are
	// done: the deactivation procedures ran, and the pods stopped.
	ConditionSafeToPowerOff = "SafeToPowerOff"
	// ConditionEditsPending is True while an edit of the outbox or of a
	// FITS keyword waits for the next session, and False while the
	// session's record holds what the specs would record now. A
	// reservation has it only while it has a session.
	ConditionEditsPending = "EditsPending"
)
