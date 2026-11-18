package observatory

import "time"

// A Reservation gives its holder the use of one Telescope. Declaring a
// device does not start it: the operator starts a telescope's pods only
// while a Reservation for that telescope is active. Activation and
// deactivation each run a fixed list of steps, and status.steps lists
// every step with its state, so `kubectl describe` shows how far the
// run got.

type Reservation = Object[ReservationSpec, ReservationStatus]

type ReservationSpec struct {
	Telescope string `json:"telescope"`
	// Holder is who uses the telescope, such as a person's desktop
	// client or astrophotography-operator.
	Holder string `json:"holder"`
	// Start is when activation begins. With no start, it begins when
	// the Reservation is created.
	Start *time.Time `json:"start,omitempty"`
	// End is when deactivation begins. With no end, the Reservation
	// lasts until it is deleted.
	End *time.Time `json:"end,omitempty"`
	// Outbox is the volume that the telescope's imaging cameras write
	// their frames to. Without an outbox, the operator does not change
	// the cameras' upload settings. A driver that a client then sets to
	// save frames locally writes them to its home directory, /tmp,
	// which is an emptyDir and is deleted with the pod.
	Outbox *Outbox `json:"outbox,omitempty"`
	// FITSKeywords are the keywords for every frame of the session.
	// MergeFITSKeywords applies them last, so they replace a keyword of
	// the same name from the Observatory, the Telescope, or the Camera.
	FITSKeywords []FITSKeyword `json:"fitsKeywords,omitempty"`
}

// Outbox names a PersistentVolumeClaim in the reservation's namespace.
// It has the shape of a pod's persistentVolumeClaim volume so that a
// field such as subPath can be added later. The person creates the
// claim, and the operator does not create one from a template, so the
// frames stay from night to night in a volume that the person chose.
type Outbox struct {
	ClaimName string `json:"claimName"`
}

// ReservationPhase summarizes a reservation's state in one word.
type ReservationPhase string

const (
	// ReservationScheduled: waiting for spec.start.
	ReservationScheduled ReservationPhase = "Scheduled"
	// ReservationActivating: the activation steps are running.
	ReservationActivating ReservationPhase = "Activating"
	// ReservationReady: the telescope is ready for the holder, and
	// status.endpoint is the server to connect to.
	ReservationReady ReservationPhase = "Ready"
	// ReservationDeactivating: the deactivation steps are running,
	// after spec.end or a delete.
	ReservationDeactivating ReservationPhase = "Deactivating"
	// ReservationReleased: the deactivation steps are done, and the
	// devices are safe to power off.
	ReservationReleased ReservationPhase = "Released"
	// ReservationFailed: a step failed. The Ready condition's message is
	// the failed step's name and summary.
	ReservationFailed ReservationPhase = "Failed"
)

// StepName identifies one step of activation or deactivation. Each
// name is unique across both lists, so a reader can select one step by
// name, as in `{.status.steps[?(@.name=="Connect")].state}`.
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

	// StepStoredAbort is the former name of StepStopWork, which
	// reservations stored by an earlier version of the operator can
	// still contain. A Released reservation keeps its steps, and the API
	// server validates them again on each update, so the CRD must
	// accept the name. The runner reads it as StepStopWork and never
	// writes it.
	StepStoredAbort StepName = "Abort"
)

// ActivationSteps run in this order. The Activation step runs the
// activation procedures of the resources. The Requires step waits for
// the spec.requires of the Telescope and its devices.
var ActivationSteps = []StepName{
	StepWait, StepStartSite, StepRequires, StepPowerOn, StepStartDevices,
	StepConnect, StepConfigure, StepActivation, StepStartGuider,
}

// DeactivationSteps run in this order, after spec.end or a delete.
var DeactivationSteps = []StepName{
	StepStopWork, StepDeactivation, StepStopGuider, StepDisconnect, StepStopDevices,
	StepPowerOff, StepStopSite,
}

// StepState is the progress of one step. A step starts only when the
// step before it is Done or Skipped.
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
	// Step is the step that is running, or the step that failed. It is
	// empty while the phase is Ready or Released, so the printer column
	// does not show a finished step as if it were running.
	Step StepName `json:"step,omitempty"`
	// Steps lists the activation steps from the start, and adds the
	// deactivation steps when deactivation begins.
	Steps []Step `json:"steps,omitempty"`
	// Endpoint is the telescope's INDI server. It is set while the
	// phase is Ready.
	Endpoint   *Endpoint   `json:"endpoint,omitempty"`
	Session    *Session    `json:"session,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Session records the specs that a reservation's session started
// with, so a spec edit during the session does not change a server
// that is running. The StartSite step writes Observatory and Site, and
// the PowerOn step writes the rest. Each part is written once, before
// the step creates a pod. When a failed activation step is retried, it
// writes its parts again from the specs as they are at that time. The
// record is kept in the reservation and not on the pods, so an
// eviction that deletes every pod of a server does not lose it.
type Session struct {
	// Observatory is the observatory of the reservation's telescope.
	// The StartSite step writes it together with Site, so a record
	// with an observatory and no Site is an observatory that has no
	// devices.
	Observatory string `json:"observatory,omitempty"`
	// Site holds each device of the observatory's server, keyed as in
	// Dome/lab. Reservations of several telescopes in one observatory
	// share its server. Each one records the devices that the server
	// ran when its StartSite step began.
	Site map[string]SessionDevice `json:"site,omitempty"`
	// Devices holds each device of the telescope's server, keyed as in
	// Camera/east-main.
	Devices map[string]SessionDevice `json:"devices,omitempty"`
	// Trains, Tubes, and Guiders hold the specs of the telescope's
	// OpticalTrains and Guiders, and of the OpticalTubes of those
	// trains, keyed by name.
	Trains  map[string]SessionTrain  `json:"trains,omitempty"`
	Tubes   map[string]SessionTube   `json:"tubes,omitempty"`
	Guiders map[string]SessionGuider `json:"guiders,omitempty"`
	// Outbox is the reservation's spec.outbox as the session started
	// it. A pod's volume claim cannot change without a new pod, and a
	// new pod in the middle of an exposure loses the exposure, so an
	// edit takes effect in the next session.
	Outbox *Outbox `json:"outbox,omitempty"`
	// FITSKeywords holds the merged keywords of each imaging camera
	// whose merged list is not empty, keyed by the camera's key, as in
	// Camera/east-main. The Configure step writes them, and the driver
	// adds each keyword it receives to every later frame until it
	// exits. Recording them keeps the declared keywords the same on
	// every frame of one session, whatever a person edits during it.
	FITSKeywords map[string][]FITSKeyword `json:"fitsKeywords,omitempty"`
}

// SessionTrain, SessionTube, and SessionGuider hold the spec of one
// OpticalTrain, OpticalTube, or Guider as the session started it. UID
// tells the recorded object apart from a new object of the same name
// that a person creates during the session.
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

// SessionDevice holds the spec of one device as the session started
// it. UID has the same purpose as in SessionTrain.
type SessionDevice struct {
	UID          string `json:"uid,omitempty"`
	Telescope    string `json:"telescope,omitempty"`
	OpticalTrain string `json:"opticalTrain,omitempty"`
	Observatory  string `json:"observatory,omitempty"`
	DeviceSpec
	// Guides is true for a camera that a Guider of the telescope uses
	// to guide.
	Guides bool `json:"guides,omitempty"`
}

// Step is the record of one step. An operator that restarts resumes
// from this record and from what it observes in the cluster, not from
// memory.
type Step struct {
	Name      StepName   `json:"name"`
	State     StepState  `json:"state"`
	StartTime *time.Time `json:"startTime,omitempty"`
	StopTime  *time.Time `json:"stopTime,omitempty"`
	// Summary says what the step is waiting for or did, and names the
	// device when one device delays the step. `kubectl describe` prints
	// a map's fields sorted by JSON name, so a printed step starts with
	// its name, except a step with actions, which starts with them.
	Summary string `json:"summary,omitempty"`
	// Actions copies the actions of the procedures that the
	// Activation or Deactivation step waited on, each with the kind
	// and name of the resource that it belongs to.
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
	// done: the deactivation procedures ran and the pods stopped.
	ConditionSafeToPowerOff = "SafeToPowerOff"
	// ConditionEditsPending is True while an edit of the outbox or of a
	// FITS keyword waits for the next session, and False while the
	// session's record matches what the specs would record now. A
	// reservation has this condition only while it has a session.
	ConditionEditsPending = "EditsPending"
)
