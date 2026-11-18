package main

// When this machine stops publishing an endpoint, for example because
// a USB card was unplugged, the sweep marks its Sink or Source absent.
// It never deletes the resource, because the spec holds what a person
// declared, such as the volume level. So a USB card that is unplugged
// for an hour comes back at the declared level, and a speaker that
// moves to another machine keeps its Sink. The sweep sets the Connected
// and Ready conditions to False and the connected, ready, and claimed
// gauges to 0. A failed observation leaves the gauges alone, because
// it does not show that the endpoint is gone.
//
// Only the machine that status.node names sweeps a resource, and the
// sweep leaves status.node unchanged. So when no machine publishes an
// endpoint, the last machine that wrote its resource marks it absent,
// and no other machine writes it.

import (
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// present is the set of endpoint names that this machine publishes.
// sweep continues past an error and returns every error, joined.
func (e *endpointControl) sweep(present map[string]bool) error {
	if !e.sweepDue(present) {
		return nil
	}
	var failures []error
	// After a conflict, settleSinkStatus calls absent again with the
	// current copy of the resource. By then status.node can name another
	// machine, for example when a Bluetooth speaker connected elsewhere,
	// and then absent returns false so that nothing is written.
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

// sweepDue returns true when the set of endpoints differs from the set
// at the last sweep, so a card that leaves is marked absent on the
// same pass, and otherwise once per backstopInterval.
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

// absentStatus keeps status.node, card, pcm, and the other fields that
// identify the hardware that the resource is for.
func absentStatus(published EndpointStatus, now time.Time) EndpointStatus {
	status := published
	status.NodeName = ""
	status.Format = nil
	status.Claims = nil
	status.Conditions = setCondition(status.Conditions, condition(ConnectedCondition, false,
		"EndpointAbsent", "This machine no longer publishes the endpoint", now))
	status.Conditions = setCondition(status.Conditions, condition(ReadyCondition, false,
		"NoNode", "PipeWire has no node for this endpoint", now))
	return status
}
