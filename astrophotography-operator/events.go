package main

// The Events that astrophotography-operator posts, so that `kubectl
// describe` shows what happened to a resource in the last hour without
// a look at the operator's log.
//
//   - Each condition transition of a Target or a Solver posts one Event
//     on that resource, with the condition's reason and message.
//   - A runner posts the Events below on its Reservation. Their reasons
//     start with Astrophotography because observatory-operator owns the
//     Reservation.
//   - An exposure or a dither posts no Event, so a Target gets a few
//     Events in a night, not one for each frame.

const (
	// A Warning when the reservation has no outbox. The runner then takes
	// no images, because a frame must be saved to a claim that outlives
	// the camera's pod.
	reasonNoOutbox = "AstrophotographyNoOutbox"
	// Normal, when the runner unparked the mount before a slew.
	reasonUnparked = "AstrophotographyUnparked"
	// Normal, when no target in the namespace is eligible.
	reasonIdle = "AstrophotographyIdle"
	// A Warning with the scheduler's message when the reservation's
	// worker pod cannot start. A common cause is an outbox claim with the
	// access mode ReadWriteOncePod.
	reasonWorkerPending = "AstrophotographyWorkerPending"
	// A Warning when the outbox has less free space than three frames
	// need, so the runner does not expose.
	reasonOutboxFull = "AstrophotographyOutboxFull"
	// A Warning when the target's Solver is missing or not Ready. The
	// runner then images the target without centering it.
	reasonNotCentered = "AstrophotographyNotCentered"
)

// reasonReportLost is Normal, on a Target. The pod of a succeeded
// promotion Job is gone, and the Job's report was that pod's
// termination message. So the promotion loop deleted the Job, and the
// next pass creates it again.
const reasonReportLost = "PromotionReportLost"

// reasonPromotionRetry is Normal, on a Target. A person set the
// annotation astrophotography.liken.sh/retry, so the promotion loop
// starts a Job for the frames that earlier Jobs left in the outbox
// (promotionretry.go).
const reasonPromotionRetry = "PromotionRetry"
