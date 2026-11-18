package main

// A capture request posts a Normal Event with the reason Captured on
// the Sink or Source when it sends the first block of sound. The
// message names the caller, the aspect, and the format, so kubectl
// describe sink shows who listened and when. The request's log line
// has the rest of the detail, and it never contains the token.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/events"
)

// apiComponent names the audio-api process in the liken_build_info
// metric and as the reporting component of its Events.
const apiComponent = "audio-api"

// operatorComponent names the operator container as the reporting
// component of its Events.
const operatorComponent = "audio-operator"

// endpointReference identifies a Sink or Source as the object of an
// Event. kubectl describe selects a resource's Events by UID, so an
// Event without a UID appears in kubectl get events and not in the
// output of kubectl describe.
func endpointReference(kind, name, uid string) events.ObjectReference {
	return events.ObjectReference{APIVersion: EndpointAPIVersion, Kind: kind, Name: name, UID: uid}
}

// recordCapture posts a Captured Event. The recorder queues the Event
// and returns, and a failure to post does not fail the request, because
// the sound is already sent and the log line records the same facts.
func (s *apiServer) recordCapture(kind, name, uid, aspect, format, who string) {
	s.recorder.Normal(endpointReference(kind, name, uid), reasonCaptured,
		fmt.Sprintf("%s captured the %s of this %s as %s", who, aspect, kind, format))
}
