package main

// The condition types and reasons are part of the published interface.
// People match them in `kubectl describe` and `kubectl events`, and the
// guides name them, so do not rename one.

// The Receiver conditions and their reasons.
const (
	reachableConditionType = "Reachable"
	reasonConnected        = "Connected"
	reasonUnreachable      = "Unreachable"
	reasonConnecting       = "Connecting"

	settingsConfirmedConditionType = "SettingsConfirmed"
	reasonNotConfirmed             = "NotConfirmed"

	inputSelectedConditionType = "InputSelected"
	reasonSessionInput         = "SessionInput"
	reasonOtherInput           = "OtherInput"
	reasonNoInputReported      = "NoInputReported"
)

// The Events for a change of System Audio Mode in a Receiver's
// status.cec. Both are Normal, because a receiver turns the mode off
// before it enters standby, and a person turns it off by choosing the
// TV's speakers.
const (
	reasonSystemAudioModeOn  = "SystemAudioModeOn"
	reasonSystemAudioModeOff = "SystemAudioModeOff"
)

// The CECBus conditions and their reasons.
const (
	conditionAddressKnown = "AddressKnown"
	conditionJoined       = "Joined"
	conditionCoherent     = "Coherent"
	conditionScanned      = "Scanned"

	reasonListening   = "Listening"
	reasonNotReported = "NotReported"
	reasonRefused     = "Refused"
	reasonNoAddress   = "NoAddress"
	reasonNoLogical   = "NoLogicalAddress"
	reasonOneAdapter  = "OneAdapter"
	reasonApart       = "Apart"
	reasonScanning    = "Scanning"
	reasonNoAnswer    = "NoAnswer"
	reasonSilent      = "Silent"
	reasonStale       = "Stale"
	reasonStopped     = "Stopped"
)

// The Television conditions and their reasons. The operator's Deployment
// writes Reachable and InCharge, and the node workloads write the others.
const (
	conditionReachable          = "Reachable"
	conditionInCharge           = "InCharge"
	conditionPowerApplied       = "PowerApplied"
	conditionWakeApplied        = "WakeApplied"
	conditionSystemAudioApplied = "SystemAudioApplied"
	conditionStandbyApplied     = "StandbyApplied"

	reasonInCharge        = "InCharge"
	reasonAnotherInCharge = "AnotherInCharge"

	reasonAnswers         = "Answers"
	reasonNoBus           = "NoBus"
	reasonNotScanned      = "NotScanned"
	reasonNotFound        = "NotFound"
	reasonNoPower         = "NoPowerStatus"
	reasonConfirmed       = "Confirmed"
	reasonUnconfirmed     = "Unconfirmed"
	reasonTaken           = "SourceTaken"
	reasonChosen          = "Chosen"
	reasonTooLate         = "TooLate"
	reasonSuperseded      = "Superseded"
	reasonWaking          = "Waking"
	reasonEnteringStandby = "EnteringStandby"
)

// The Events for creating and deleting a discovered Television. They are
// posted on the CECBus where discovery found the TV, because `kubectl
// describe` cannot show the Events of a Television that is deleted.
const (
	reasonTelevisionCreated = "TelevisionCreated"
	reasonTelevisionDeleted = "TelevisionDeleted"
)

// warningReasons are the reasons that a person may need to act on, such
// as equipment that does not answer, a failed adapter or cable, or a
// command that the TV did not confirm. The Event of any other reason is
// Normal, because it is an expected state, such as a scan in progress or
// a wake that a person ended by choosing another source.
var warningReasons = map[string]bool{
	reasonUnreachable:  true,
	reasonNotConfirmed: true,
	reasonNotReported:  true,
	reasonRefused:      true,
	reasonNoAddress:    true,
	reasonNoLogical:    true,
	reasonApart:        true,
	reasonNoAnswer:     true,
	reasonStale:        true,
	reasonNoBus:        true,
	reasonNotFound:     true,
	reasonNoPower:      true,
	reasonUnconfirmed:  true,
	reasonTaken:        true,
	reasonTooLate:      true,
}
