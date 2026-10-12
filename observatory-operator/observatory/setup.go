package observatory

// A person's part in a procedure. A portable telescope spends the day
// in a garage, and a person carries it out, uncovers it, and polar
// aligns it before a night. The person states that in the resource's
// status, as the SetUp condition, and a spec.requires that names it
// keeps the equipment off until then. Plan 21 gives the design.

// ConditionSetUp is the condition that a person writes into a
// resource's status.conditions through the status subresource, such as
// with `kubectl liken observatory set-up`. True says that the person
// set the resource up for the night. The operator keeps it as the
// person wrote it, and sets it False with the reason Expired at the
// first astronomical dawn after its lastTransitionTime at the
// resource's Observatory, or at status.setUpUntil when the person
// wrote one. A confirmation is an observation of the world, so it is
// in status, where a GitOps tool that applies the spec never removes
// it.
const ConditionSetUp = "SetUp"

// The reasons of SetUp. A person writes SetUp and TakenDown, and the
// operator writes Expired.
const (
	ReasonSetUp     = "SetUp"
	ReasonTakenDown = "TakenDown"
	ReasonExpired   = "Expired"
)

// ConditionAttentionNeeded is True while a notify action of the
// resource's procedures asks a person to do something, with the text
// of each request as its message. It turns False at the start of the
// resource's next activation, or when a person writes SetUp after the
// request, as `kubectl liken observatory take-down` does once the
// telescope is covered. A resource that never ran a notify action has
// no such condition.
const ConditionAttentionNeeded = "AttentionNeeded"

// The reasons of AttentionNeeded.
const (
	ReasonNotified     = "Notified"
	ReasonAcknowledged = "Acknowledged"
	ReasonActivated    = "Activated"
)
