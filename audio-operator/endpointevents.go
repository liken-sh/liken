package main

// Each condition transition of a Sink or a Source posts one Event,
// after the status write that records it succeeds. When the API server
// refuses the write, nothing is posted, and the next pass finds the
// same transition again. Actions and faults that change no condition
// post the reasons in eventreasons.go.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// transitions returns the conditions in after that are new or have a
// different status or reason. A changed message alone is not a
// transition, as in conditions.Set.
func transitions(before, after []EndpointCondition) []EndpointCondition {
	var changed []EndpointCondition
	for _, next := range after {
		previous, found := conditions.Find(before, next.Type)
		if !found || previous.Status != next.Status || previous.Reason != next.Reason {
			changed = append(changed, next)
		}
	}
	return changed
}

// warningStatus returns the status that makes the Event of c a
// Warning, or "" when no status of c needs a person.
//
// Only Ready False while Connected is True is a Warning: a device is
// connected, but PipeWire has no node to play to it. Connected False is
// Normal, because a person turns off a television or unplugs a jack on
// purpose. LayoutApplied is False only while the operator waits for the
// endpoint to go idle or restarts PipeWire.
func warningStatus(status EndpointStatus, c EndpointCondition) conditions.Status {
	if c.Type != ReadyCondition {
		return ""
	}
	if connected, _ := conditions.Find(status.Conditions, ConnectedCondition); connected.Status != conditionTrue {
		return ""
	}
	return conditionFalse
}

func (e *endpointControl) postTransitions(object events.ObjectReference, published, written EndpointStatus) {
	for _, c := range transitions(published.Conditions, written.Conditions) {
		e.recorder.Transition(object, c, warningStatus(written, c))
	}
}

// sinkReference returns the object reference for an Event about a
// Sink. When the Sink cannot be read, the reference has no UID.
func (e *endpointControl) sinkReference(name string) events.ObjectReference {
	uid := ""
	if sink, err := informer.ReadOne[Sink](e.client, e.cache.sinks, name, sinkPath(name)); err == nil {
		uid = sink.Metadata.UID
	}
	return endpointReference(SinkKind, name, uid)
}

func (e *endpointControl) warnSinks(names []string, reason, message string) {
	if e == nil || e.recorder == nil {
		return
	}
	for _, name := range names {
		e.recorder.Warning(e.sinkReference(name), reason, message)
	}
}

func (e *endpointControl) noteSinks(names []string, reason, message string) {
	if e == nil || e.recorder == nil {
		return
	}
	for _, name := range names {
		e.recorder.Normal(e.sinkReference(name), reason, message)
	}
}
