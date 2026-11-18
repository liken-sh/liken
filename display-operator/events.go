package main

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/events"
)

// recordCapture posts a Captured Event on the Display, so
// `kubectl describe display` shows who captured the screen and when.
// The API's log line of the request has the route, status, bytes,
// duration, and request ID. The recorder queues the Event and returns,
// so a failed write cannot fail the request.
func recordCapture(recorder *events.Recorder, screen *Display, subject, aspect, form string) {
	name := screen.Metadata.Name
	recorder.Normal(displayReference(screen), CapturedReason,
		fmt.Sprintf("%s captured the %s of %s as %s", subject, aspect, name, form))
}
