package main

// A remote's volume key is a press on the media bus. media-operator turns
// each press into an absolute level and writes it, with the time of the
// press, into the Sink's status.session.volumeAsk by server-side apply.
// This operator applies each new request once, with the same level write
// as a declared level, and then reports the device's level in
// status.observed. It does not write the request's level again, so a
// level set later with the speaker's own button stays.
//
// The time of the press, VolumeAsk.At, identifies a request, so each new
// time is a new request:
//
//   - The operator records the request that it finds the first time it reads
//     a Sink, and does not apply it. That read happens in the first pass
//     after a start, or when the Sink's status.node changes to this
//     machine. The operator that ran before applied that request, or a press
//     of the speaker's own button came after it, so the device's level
//     is newer.
//   - When requests arrive faster than the operator applies them, only the
//     newest is applied, because a read of the Sink returns only the
//     newest request.
//   - A request for a Sink whose endpoint has no node is dropped, because
//     there is no node to write. When the node appears, the pass writes
//     the declared level to it (resting.go).
//
// A request does not wait for settleWindow (main.go). The window collects
// a burst of hardware events into one ResourceSlice write, and a level
// write changes no ResourceSlice. A held key sends a request on each
// repeat, and the window would apply only the last of them.

import (
	"context"
	"fmt"
	"os"
)

func (s *Sink) volumeAsk() *VolumeAsk {
	if s.Status.Session == nil {
		return nil
	}
	return s.Status.Session.VolumeAsk
}

func askTime(sink *Sink) string {
	if ask := sink.volumeAsk(); ask != nil {
		return ask.At
	}
	return ""
}

// noteAsk records the Sink's current request the first time the operator
// reads the Sink, so applyAsks never applies it. Later calls change
// nothing, because only applyAsks updates the record.
func (e *endpointControl) noteAsk(sink *Sink) {
	if _, seen := e.asks[sink.Metadata.Name]; !seen {
		e.asks[sink.Metadata.Name] = askTime(sink)
	}
}

// reportAsk writes the level that a request set into status.observed. It
// changes only status.observed.volume and status.observed.mute, and
// keeps the rest of the published status.
//
// media-operator treats a pressed level as pending until the Sink
// reports it, and stops waiting after one second. The pass that reads
// the graph runs after settleWindow, 1.5 s or more after the write,
// which is too late. The level write succeeded, so the level is the
// device's level, and the next pass replaces it with what PipeWire
// reports.
func (e *endpointControl) reportAsk(sink *Sink, level levelWrite) error {
	return e.settleSinkStatus(sink, func(published EndpointStatus) (EndpointStatus, bool) {
		observed := EndpointObserved{}
		if published.Observed != nil {
			observed = *published.Observed
		}
		observed.Volume, observed.Mute = level.Volume, level.Mute
		status := published
		status.Observed = &observed
		return status, true
	})
}

// applyAsks applies each new request of this machine's Sinks and reports
// whether it wrote a level. It writes the level with applyLevel, to the
// node or the speaker's Route that the last pass read.
//
// It also sets the nodeRecord's written level, because an idle node
// reports no change in the graph, and the pass then reports the written
// level in status.observed (sinkstatus.go). It leaves the nodeRecord's
// reconciled level as it is, so the next pass does not treat the request
// as a change to the spec and write the spec back.
func (e *endpointControl) applyAsks(ctx context.Context) bool {
	sinks, err := e.readSinks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the Sinks for their volume requests: %v\n", err)
		return false
	}
	applied := false
	for index := range sinks {
		sink := &sinks[index]
		name := sink.Metadata.Name
		last, seen := e.asks[name]
		e.asks[name] = askTime(sink)
		ask := sink.volumeAsk()
		if !seen || ask == nil || ask.At == last {
			continue
		}
		facts, present := e.latest[name]
		if !present || !facts.HasNode {
			fmt.Fprintf(os.Stderr, "%s: dropped the volume request at %s, because the Sink's endpoint has no node\n", name, ask.At)
			continue
		}
		level := levelWrite{Volume: &ask.Level, Mute: &ask.Mute}
		if err := e.applyLevel(ctx, facts, level); err != nil {
			e.readings.controlFailed(operationVolume)
			fmt.Fprintf(os.Stderr, "%s: applying the volume request at %s: %v\n", name, ask.At, err)
			continue
		}
		fmt.Printf("%s: set %s for the volume request at %s\n", name, level, ask.At)
		if record, known := e.nodes[name]; known {
			record.written = &level
			e.nodes[name] = record
		}
		if err := e.reportAsk(sink, level); err != nil {
			fmt.Fprintf(os.Stderr, "%s: reporting the volume request at %s: %v\n", name, ask.At, err)
		}
		applied = true
	}
	return applied
}
