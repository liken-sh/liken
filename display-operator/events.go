package main

// This file writes the record a capture leaves behind. An Event on
// the Display is the record, because kubectl describe display then
// answers who looked at a screen and when with no log to open, in
// the same place every other fact about the Display is read. The
// API's log line is the detail beside it: the route, the status, the
// bytes, the duration, and the request id.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/events"
)

// recordCapture posts the Captured Event of one capture, with the
// subject and the aspect in its message. The recorder queues the
// Event and returns, so a failed write never fails the request: the
// bytes already reached the caller.
func recordCapture(recorder *events.Recorder, screen *Display, subject, aspect, form string) {
	name := screen.Metadata.Name
	recorder.Normal(displayReference(screen), CapturedReason,
		fmt.Sprintf("%s took the %s of %s as %s", subject, aspect, name, form))
}
