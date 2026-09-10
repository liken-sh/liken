package main

// The sweep: what a pass says about a Sink or a Source whose endpoint
// this machine no longer publishes, such as a USB card that was
// unplugged.
//
// The resource is never deleted, because it holds the declaration a
// person wrote. A USB card that is unplugged for an hour must come
// back to the level it rested at, and a speaker that moved to another
// machine keeps its Sink. The conditions are what report the absence,
// and the hardware triple's gauges report it too, because a sweep is a
// confirmed absence, not an invalid observation.

import (
	"errors"
	"maps"
	"slices"
	"time"
)

// sweep reports the endpoints this machine held a resource for and no
// longer publishes.
func (e *endpointControl) sweep(present map[string]bool) error {
	if !e.sweepDue(present) {
		return nil
	}
	var failures []error
	sinks, err := listSinks(e.client)
	if err != nil {
		failures = append(failures, err)
	}
	for _, sink := range sinks {
		if sink.Status.Node != e.machine || present[sink.Metadata.Name] {
			continue
		}
		status := absentStatus(sink.Status, e.now())
		e.readings.endpoint(sink.Metadata.Name, false, false, false)
		if sameStatus(sink.Status, status) {
			continue
		}
		if _, err := writeSinkStatus(e.client, &sink, status); err != nil {
			failures = append(failures, err)
		}
	}
	sources, err := listSources(e.client)
	if err != nil {
		failures = append(failures, err)
	}
	for _, source := range sources {
		if source.Status.Node != e.machine || present[source.Metadata.Name] {
			continue
		}
		status := absentStatus(source.Status, e.now())
		e.readings.endpoint(source.Metadata.Name, false, false, false)
		if sameStatus(source.Status, status) {
			continue
		}
		if _, err := writeSourceStatus(e.client, &source, status); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// sweepDue answers whether this pass lists the resources. It does
// when the endpoints this machine publishes are not the endpoints of
// the last listing, so a card that arrives or leaves is answered on
// the pass that finds it, and otherwise once per backstop interval.
func (e *endpointControl) sweepDue(present map[string]bool) bool {
	names := slices.Sorted(maps.Keys(present))
	if !slices.Equal(names, e.swept) {
		e.swept, e.sweptAt = names, e.now()
		return true
	}
	if e.now().Before(e.sweptAt.Add(backstopInterval)) {
		return false
	}
	e.sweptAt = e.now()
	return true
}

// absentStatus is what a resource reports once its machine no longer
// publishes the endpoint. The identity it read stays, because it says
// which piece of hardware this resource is for.
func absentStatus(published EndpointStatus, now time.Time) EndpointStatus {
	status := published
	status.NodeName = ""
	status.Format = nil
	status.Claim = nil
	status.Conditions = setCondition(status.Conditions, condition(ConnectedCondition, false,
		"EndpointAbsent", "this machine no longer publishes the endpoint", now))
	status.Conditions = setCondition(status.Conditions, condition(ReadyCondition, false,
		"NoNode", "PipeWire holds no node for this endpoint", now))
	return status
}
