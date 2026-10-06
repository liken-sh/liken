package main

// The record a person reads with kubectl.
//
// Every request that produces bytes posts a Kubernetes Event on the
// Sink or the Source: reason Captured, type Normal, with the caller
// and the aspect in the message, so `kubectl describe sink` answers who
// listened and when. The log line is the detail record and never
// carries the token.

import (
	"fmt"

	"github.com/liken-sh/liken/kubernetes/events"
)

// apiComponent is this process's name on liken_build_info and on every
// Event it posts.
const apiComponent = "audio-api"

// operatorComponent is the operator container's name on every Event it
// posts.
const operatorComponent = "audio-operator"

// endpointReference names a Sink or a Source for an Event. The UID is
// what `kubectl describe` selects a resource's Events by, so an Event
// with no UID reaches `kubectl get events` but not the describe output.
func endpointReference(kind, name, uid string) events.ObjectReference {
	return events.ObjectReference{APIVersion: EndpointAPIVersion, Kind: kind, Name: name, UID: uid}
}

// recordCapture posts one Captured Event. The recorder queues it and
// never fails the request: the sound is already on the wire, and the
// log line holds the same record.
func (s *apiServer) recordCapture(kind, name, uid, aspect, format, who string) {
	s.recorder.Normal(endpointReference(kind, name, uid), reasonCaptured,
		fmt.Sprintf("%s captured the %s of this %s as %s", who, aspect, kind, format))
}
