package observatory

// Conditions for work that only a person can do, such as carrying a
// portable telescope outside, uncovering it, and polar aligning it
// before a night. A reservation does not start a resource whose
// spec.requires lists SetUp until the person sets SetUp to True.

// ConditionSetUp is a condition that a person writes to the resource's
// status.conditions through the status subresource, for example with
// `kubectl liken observatory set-up`. True means that the person set
// the resource up for the night. The operator sets it to False with the
// reason Expired at status.setUpUntil when the person wrote one, and
// otherwise at the first astronomical dawn at the resource's
// Observatory after the condition's lastTransitionTime. The condition
// is in status, not spec, so a GitOps tool that applies the spec does
// not remove it.
const ConditionSetUp = "SetUp"

// The reasons of the SetUp condition. A person writes SetUp and
// TakenDown, and the operator writes Expired.
const (
	ReasonSetUp     = "SetUp"
	ReasonTakenDown = "TakenDown"
	ReasonExpired   = "Expired"
)

// ConditionAttentionNeeded is True while a notify action in the
// resource's procedures has asked a person to act, and its message is
// the text of each open request. It becomes False when the resource's
// next activation starts, or when a person writes the SetUp condition
// after the request, as `kubectl liken observatory take-down` does once
// the telescope is covered. A resource that never ran a notify action
// does not have this condition.
const ConditionAttentionNeeded = "AttentionNeeded"

// The reasons of the AttentionNeeded condition.
const (
	ReasonNotified     = "Notified"
	ReasonAcknowledged = "Acknowledged"
	ReasonActivated    = "Activated"
)
