package main

// Every condition this operator reports, every reason a condition or an
// Event carries, and which reasons post a Warning. The reasons are part
// of the published interface: a person matches them in
// `kubectl get -o yaml`, in `kubectl describe`, and in
// `kubectl events -n default`, and the guides name them. Each
// condition transition posts one Event with the condition's reason and
// message (conditions.go).

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

// The Television conditions and their reasons. The Deployment writes
// Reachable and InCharge; the node workloads write PowerApplied,
// WakeApplied, and StandbyApplied.
const (
	conditionReachable      = "Reachable"
	conditionInCharge       = "InCharge"
	conditionPowerApplied   = "PowerApplied"
	conditionWakeApplied    = "WakeApplied"
	conditionStandbyApplied = "StandbyApplied"

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

// The Events of actions that change no condition. Each is Normal and
// posted on the CECBus whose TV discovery found, because a deleted
// Television can hold no Event of its own.
const (
	reasonTelevisionCreated = "TelevisionCreated"
	reasonTelevisionDeleted = "TelevisionDeleted"
)

// warningReasons are the reasons of a verdict that a person may need
// to act on: equipment that does not answer, an adapter or a cable
// that fails, and a command the TV did not confirm. Every other reason
// is an expected state, such as a bus in Listen, a scan that runs, an
// adapter that a rollout stopped, or a wake that a person's choice
// ended, and its Event is Normal.
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
