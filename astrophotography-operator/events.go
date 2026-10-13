package main

// The Events of astrophotography-operator. `kubectl describe` lists
// them under the status, so a person reads what happened to a resource
// in the last hour with no log to open. kubernetes/events writes them,
// and root plan 78 gives the rule for a condition, an Event, and a log
// line:
//
//   - Each condition transition of a Target or a Solver posts one Event
//     on that resource, with the condition's reason and message.
//   - The runner's actions on a Reservation post an Event on the
//     Reservation, with the reasons below. They take the prefix
//     Astrophotography, because the Reservation is observatory-operator's
//     resource.
//   - The promotion loop posts reasonReportLost on a Target when it
//     runs a Job again because the Job's report is gone.
//   - An exposure and a dither change no condition and post nothing, so
//     a target posts a few Events in a night, not one for each frame.

// The reasons of the Events on a Reservation (plans 01, 02, and 03).
const (
	// reasonNoOutbox is a Warning: the reservation has no outbox, so the
	// runner images nothing, because a frame must land in a claim that
	// outlives the camera's pod.
	reasonNoOutbox = "AstrophotographyNoOutbox"
	// reasonUnparked is Normal: the runner unparked the mount before a
	// slew.
	reasonUnparked = "AstrophotographyUnparked"
	// reasonIdle is Normal: no target of the namespace is eligible.
	reasonIdle = "AstrophotographyIdle"
	// reasonWorkerPending is a Warning: the reservation's worker pod
	// cannot start, with the scheduler's message, such as for an outbox
	// claim whose access mode is ReadWriteOncePod.
	reasonWorkerPending = "AstrophotographyWorkerPending"
	// reasonOutboxFull is a Warning: the outbox has less than three
	// frames' space free, so the runner does not expose.
	reasonOutboxFull = "AstrophotographyOutboxFull"
	// reasonNotCentered is a Warning: the target's Solver is missing or
	// not Ready, so the runner images the target without centering it.
	reasonNotCentered = "AstrophotographyNotCentered"
)

// reasonReportLost is Normal, on a Target: the pod of a promotion Job
// that succeeded is gone, and its report with it, so the loop deleted
// the Job and starts it again (plan 03).
const reasonReportLost = "PromotionReportLost"
