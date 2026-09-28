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
//
// Only the machine that status.node names sweeps a resource, and the
// sweep keeps status.node. A card's Sink under a name that no machine
// publishes any more, such as a Sink whose name carries no machine, is
// therefore reported absent once, by the last machine that wrote it,
// and no other machine writes it again.

import (
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// sweep reports the endpoints this machine held a resource for and no
// longer publishes.
func (e *endpointControl) sweep(present map[string]bool) error {
	if !e.sweepDue(present) {
		return nil
	}
	var failures []error
	// Only the machine that status.node names writes the absence. The
	// retry after a conflict reads the resource again, and a Bluetooth
	// speaker's Sink can name another machine by then.
	now := e.now()
	absent := func(published EndpointStatus) (EndpointStatus, bool) {
		return absentStatus(published, now), published.Node == e.machine
	}
	sinks, err := e.readSinks()
	if err != nil {
		failures = append(failures, err)
	}
	for _, sink := range sinks {
		if sink.Status.Node != e.machine || present[sink.Metadata.Name] {
			continue
		}
		e.readings.endpoint(sink.Metadata.Name, false, false, false)
		if err := e.settleSinkStatus(&sink, absent); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			failures = append(failures, err)
		}
	}
	sources, err := e.readSources()
	if err != nil {
		failures = append(failures, err)
	}
	for _, source := range sources {
		if source.Status.Node != e.machine || present[source.Metadata.Name] {
			continue
		}
		e.readings.endpoint(source.Metadata.Name, false, false, false)
		if err := e.settleSourceStatus(&source, absent); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
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
