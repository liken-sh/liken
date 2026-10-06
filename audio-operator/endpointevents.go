package main

// The Events this operator posts about its Sinks and Sources.
//
// Each condition transition posts one Event with the condition's
// reason and message, after the status write that carries it lands.
// A pass composes the whole status first (sinkstatus.go) and writes it
// in one merge patch, so a write that the API server refuses posts
// nothing, and the next pass finds the same transition again.
//
// The actions and faults that change no condition post the reasons in
// eventreasons.go.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// transitions answers the conditions of after that are new or whose
// status or reason differs from before. These are the conditions that
// conditions.Set reports as transitioned while the pass composes.
func transitions(before, after []EndpointCondition) []EndpointCondition {
	var changed []EndpointCondition
	for _, next := range after {
		held, found := conditions.Find(before, next.Type)
		if !found || held.Status != next.Status || held.Reason != next.Reason {
			changed = append(changed, next)
		}
	}
	return changed
}

// badStatus answers the status of one condition that needs a person,
// given the whole status the condition is part of.
//
// Connected is never a Warning: a television that turns off, a plug
// pulled from a jack, and a speaker that powers down are what a person
// does on purpose. LayoutApplied is False only while the operator
// waits for idle or restarts PipeWire, which it does on its own.
// Ready is a Warning only while Connected is True: sound can leave the
// endpoint, and PipeWire holds no node to send it through.
func badStatus(status EndpointStatus, c EndpointCondition) conditions.Status {
	if c.Type != ReadyCondition {
		return ""
	}
	if connected, _ := conditions.Find(status.Conditions, ConnectedCondition); connected.Status != conditionTrue {
		return ""
	}
	return conditionFalse
}

// postTransitions posts one Event for each condition that changed
// between the published status and the written one.
func (e *endpointControl) postTransitions(object events.ObjectReference, published, written EndpointStatus) {
	for _, c := range transitions(published.Conditions, written.Conditions) {
		e.recorder.Transition(object, c, badStatus(written, c))
	}
}

// sinkReference names a Sink for an Event, with the UID the watch
// holds for it. A Sink whose UID does not read still gets the Event,
// with no UID.
func (e *endpointControl) sinkReference(name string) events.ObjectReference {
	uid := ""
	if sink, err := informer.ReadOne[Sink](e.client, e.cache.sinks, name, sinkPath(name)); err == nil {
		uid = sink.Metadata.UID
	}
	return endpointReference(SinkKind, name, uid)
}

// warnSinks posts one Warning on each named Sink.
func (e *endpointControl) warnSinks(names []string, reason, message string) {
	if e == nil || e.recorder == nil {
		return
	}
	for _, name := range names {
		e.recorder.Warning(e.sinkReference(name), reason, message)
	}
}

// noteSinks posts one Normal Event on each named Sink.
func (e *endpointControl) noteSinks(names []string, reason, message string) {
	if e == nil || e.recorder == nil {
		return
	}
	for _, name := range names {
		e.recorder.Normal(e.sinkReference(name), reason, message)
	}
}
