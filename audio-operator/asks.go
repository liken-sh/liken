package main

// The volume asks.
//
// A remote's volume key is a press on the media bus, and the media
// operator turns each press into an absolute level for the unit's
// Sink. It writes that level into the Sink's status.session.volumeAsk,
// with the time of the ask, by server-side apply under its own field
// manager. This operator applies each new ask once, through the same
// level write a declared level takes, and then reports the level the
// device holds in status.observed. It never writes the ask's level
// again, so a press of a speaker's own button after the ask stands.
//
// An ask is an event and not a state, so it is named by its time:
// each new at is one ask. Three rules follow from that.
//
//   - An ask this operator finds the first time it reads a Sink, in
//     the first pass after a start or when a Sink enters this
//     machine's selection, is recorded and not applied. The last
//     operator applied it, or the ask is older than a press of the
//     speaker's own button, and the device's level is the newer fact.
//   - Asks that arrive faster than this operator applies them are
//     coalesced: the Sink's newest copy holds the newest ask, and only
//     that one is applied.
//   - An ask for a Sink whose endpoint has no node is dropped. The
//     device has no level to set, and when it appears again it takes
//     the declared level (resting.go).
//
// An ask does not wait for the settle window in main.go. The window
// gathers a burst of hardware events into one ResourceSlice write,
// and a level write changes no ResourceSlice. A held key makes about
// one ask every 100 ms, and a window of 1.5 s would apply only the
// last of them.

import (
	"context"
	"fmt"
	"os"
)

// volumeAsk answers the Sink's ask, and nil when it holds none.
func (s *Sink) volumeAsk() *VolumeAsk {
	if s.Status.Session == nil {
		return nil
	}
	return s.Status.Session.VolumeAsk
}

// askTime answers the time that names the Sink's ask, and an empty
// string when the Sink holds no ask.
func askTime(sink *Sink) string {
	if ask := sink.volumeAsk(); ask != nil {
		return ask.At
	}
	return ""
}

// noteAsk records the ask a Sink holds the first time this operator
// reads the Sink, so that ask is never applied. A later read records
// nothing, because applyAsks is what moves the record forward.
func (e *endpointControl) noteAsk(sink *Sink) {
	if _, seen := e.asks[sink.Metadata.Name]; !seen {
		e.asks[sink.Metadata.Name] = askTime(sink)
	}
}

// reportAsk writes the level an ask set into status.observed, as a
// merge patch of the volume and the mute alone.
//
// The media operator holds a pressed level as pending until the Sink
// reports it, and gives up after one second. The pass that reads the
// graph comes through the settle window, 1.5 s or more after the
// write, so the level the ask wrote is reported here at once. The
// write landed, so the level is the device's level, and the pass that
// follows replaces it with what PipeWire reports.
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

// applyAsks applies the new ask of every Sink of this machine, and
// reports whether it wrote a level.
//
// The level goes to the endpoint the last pass read: its node, or a
// speaker's Route. The level is also recorded as written on the node,
// because an idle node announces no change, and status.observed then
// reports the level the ask set (sinkstatus.go). The level goes into
// status.observed at once as well (reportAsk). The node's held
// declaration stays as it was, so the next pass does not read the ask
// as a drift from the spec and write the spec back.
func (e *endpointControl) applyAsks(ctx context.Context) bool {
	sinks, err := e.readSinks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the Sinks for their volume asks: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "%s: the volume ask at %s finds no node, and is dropped\n", name, ask.At)
			continue
		}
		level := levelWrite{Volume: &ask.Level, Mute: &ask.Mute}
		if err := e.applyLevel(ctx, facts, level); err != nil {
			e.readings.controlFailed(operationVolume)
			fmt.Fprintf(os.Stderr, "%s: applying the volume ask at %s: %v\n", name, ask.At, err)
			continue
		}
		fmt.Printf("%s: %s, asked at %s\n", name, level, ask.At)
		if record, held := e.nodes[name]; held {
			record.written = &level
			e.nodes[name] = record
		}
		if err := e.reportAsk(sink, level); err != nil {
			fmt.Fprintf(os.Stderr, "%s: reporting the volume ask at %s: %v\n", name, ask.At, err)
		}
		applied = true
	}
	return applied
}
