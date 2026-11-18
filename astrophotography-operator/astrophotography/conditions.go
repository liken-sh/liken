package astrophotography

// The condition types of Target and Solver, and the reasons each one
// reports. Each transition of a condition posts one Event.

// The conditions of a Target.
const (
	// ConditionComplete is True when every sequence has archived at
	// least its count of frames.
	ConditionComplete = "Complete"
	// ConditionImaging is True while a Reservation images the Target,
	// and its reason is the current step.
	ConditionImaging = "Imaging"
	// ConditionArchiveAvailable is True when the last promotion
	// succeeded.
	ConditionArchiveAvailable = "ArchiveAvailable"
)

// The reasons of Complete.
const (
	ReasonArchived = "Archived"
	ReasonWorkLeft = "WorkLeft"
)

// The reasons of Imaging. Slewing, Centering, Guiding, and Exposing
// are the steps while the condition is True. The others give the
// reason it is False.
const (
	ReasonSlewing         = "Slewing"
	ReasonCentering       = "Centering"
	ReasonGuiding         = "Guiding"
	ReasonExposing        = "Exposing"
	ReasonWaiting         = "Waiting"
	ReasonSuspended       = "Suspended"
	ReasonSequencesDone   = "SequencesDone"
	ReasonNoTrain         = "NoTrain"
	ReasonNoFilter        = "NoFilter"
	ReasonSlewFailed      = "SlewFailed"
	ReasonCenteringFailed = "CenteringFailed"
	ReasonGuidingFailed   = "GuidingFailed"
	ReasonOutboxFull      = "OutboxFull"
	// ReasonExposureFailed means the last exposure produced no frame.
	// When the camera failed, the runner keeps the Target and starts the
	// next exposure after a wait. When a sequence's settings do not
	// parse, the runner releases the Target and records status.failed.
	ReasonExposureFailed = "ExposureFailed"
	// ReasonDeleting means the Target is deleted but frames of it are
	// still in an outbox. No Reservation claims it, and its finalizer
	// keeps it until promotion moves those frames.
	ReasonDeleting = "Deleting"
	// ReasonBelowHorizon means the Target is below the telescope's
	// horizon. No runner claims it, and the runner that had claimed it
	// releases it before the next frame.
	ReasonBelowHorizon = "BelowHorizon"
)

// The reasons of ArchiveAvailable.
const (
	ReasonPromoted      = "Promoted"
	ReasonClaimMissing  = "ClaimMissing"
	ReasonUnschedulable = "Unschedulable"
	ReasonCopyFailed    = "CopyFailed"
	ReasonVerifyFailed  = "VerifyFailed"
	ReasonNameConflict  = "NameConflict"
)

// The condition of a Solver and its reasons. ReasonClaimMissing is
// also a reason of ArchiveAvailable.
const (
	ConditionReady     = "Ready"
	ReasonLoaded       = "Loaded"
	ReasonNoIndexFiles = "NoIndexFiles"
	ReasonPodNotReady  = "PodNotReady"
)
