package astrophotography

// The conditions of each kind and the reasons they carry, from plans
// 01, 02, and 03. Each transition posts one Event, under the rule of
// root plan 78.

// The conditions of a Target.
const (
	// ConditionComplete is True when every sequence has at least its
	// count of frames archived.
	ConditionComplete = "Complete"
	// ConditionImaging is True while a reservation images the target,
	// and its reason names the step.
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

// The reasons of Imaging. The first four are the steps of a True
// condition, and the rest say why it is False.
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
	// ReasonExposureFailed says that the last exposure gave no frame:
	// the driver answered Alert, did not finish in time, or saved the
	// frame under another name. The runner keeps the target and tries
	// the next frame after a wait.
	ReasonExposureFailed = "ExposureFailed"
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

// The condition of a Solver and its reasons. ClaimMissing is shared
// with ArchiveAvailable.
const (
	ConditionReady     = "Ready"
	ReasonLoaded       = "Loaded"
	ReasonNoIndexFiles = "NoIndexFiles"
	ReasonPodNotReady  = "PodNotReady"
)
