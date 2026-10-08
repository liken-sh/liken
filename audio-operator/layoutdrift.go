package main

// Applying a new channel layout to a running PipeWire.
//
// PipeWire reads the declaration once, while it loads its
// configuration, and WirePlumber reads a node's formats once, when it
// configures the node. So a sink takes a new layout only from a new
// PipeWire. The operator writes the new declaration over the old one,
// and the PipeWire container's first process restarts PipeWire in place
// when the drop-in is newer than the socket PipeWire created at its
// start (declarationloaded.go, restarts.go). WirePlumber exits when its
// PipeWire goes away, and its container's first process starts it
// again, so it configures every node again. The pod stays,
// and so do the operator, its watches, and its claims.
//
// A layout difference is one of two things: a spec.layout that
// changed, or an HDMI output that now has a valid ELD whose layout
// differs from the declared one. An ELD that becomes absent is not a
// difference (selectLayout), so a television that turns off changes
// nothing.
//
// The restart ends every stream PipeWire carries, on every card and
// every speaker of the machine, because one PipeWire serves them all.
// So the operator writes the new declaration only while no link
// touches any node it publishes, and never in the middle of a film.

import (
	"fmt"
	"maps"
	"os"
	"time"
)

// layoutRestartGrace is how long after writing a new declaration a
// graph read that fails is the restart and not a PipeWire that stopped
// answering. The PipeWire container's first process restarts PipeWire
// within a second or two of the write. A minute covers that with room,
// and a PipeWire that is still silent after it counts toward
// maxSinkFailures again.
const layoutRestartGrace = time.Minute

// The reasons LayoutApplied takes.
const (
	layoutReasonApplied      = "Applied"
	layoutReasonAwaitingIdle = "AwaitingIdle"
	layoutReasonRestarting   = "Restarting"
)

// layoutState is what one sink's status reports about its layout: the
// layout the declaration holds, and whether PipeWire runs it.
type layoutState struct {
	Layout  channelLayout
	Applied bool
	Reason  string
	Message string
}

// layoutChange is one sink whose selected layout differs from the
// declared one.
type layoutChange struct {
	Endpoint alsaEndpoint
	From, To channelLayout
}

// planLayouts compares the layout each declared sink would be given
// now with the one it was declared with. An endpoint the declaration
// holds no node for is left out: it is a PCM device that appeared
// since PipeWire started, and only a replacement pod declares it.
func planLayouts(declared map[nodeAddress]channelLayout, endpoints []alsaEndpoint,
	specs map[nodeAddress][]string) []layoutChange {
	var changes []layoutChange
	for _, endpoint := range endpoints {
		address := endpoint.graphAddress()
		running, isDeclared := declared[address]
		if endpoint.Capture || !isDeclared {
			continue
		}
		if wanted := selectLayout(endpoint, specs[address], running); !wanted.equal(running) {
			changes = append(changes, layoutChange{Endpoint: endpoint, From: running, To: wanted})
		}
	}
	return changes
}

// reconcileLayouts applies the layout changes this pass finds, and
// answers the layout state of every declared sink, keyed by device
// name. A pass that cannot read the Sinks changes no layout, because
// a spec that did not read must not read as a spec that was removed.
// A pass that cannot read the declaration answers nothing.
func (r *reconciler) reconcileLayouts(endpoints []alsaEndpoint, graph pwGraph) map[string]layoutState {
	nodes, err := parseDeclaration(r.declared)
	if err != nil {
		r.reportLayout(fmt.Sprintf("reading the declaration PipeWire loaded: %v", err))
		return nil
	}
	declared := declaredLayouts(nodes)
	specs, err := r.specLayouts(endpoints)
	if err != nil {
		r.reportLayout(fmt.Sprintf("%v; the layouts stay as declared", err))
		return r.layoutStates(endpoints, declared, nil)
	}
	r.reportLayout("")

	changes := planLayouts(declared, endpoints, specs)
	waiting := map[string]layoutChange{}
	switch {
	case len(changes) == 0:
	case graph.playing():
		for _, change := range changes {
			waiting[change.Endpoint.Name()] = change
		}
	default:
		next, err := r.applyLayouts(nodes, declared, changes)
		if err != nil {
			r.reportLayoutWrite(changes, err)
			for _, change := range changes {
				waiting[change.Endpoint.Name()] = change
			}
			break
		}
		r.layoutWriteFailure = ""
		declared = next
	}
	return r.layoutStates(endpoints, declared, waiting)
}

// applyLayouts writes the declaration with every change in it, and
// records each change once in the log and once as an Event on its
// Sink. It answers the layouts the new declaration holds.
func (r *reconciler) applyLayouts(nodes []declaredNode, declared map[nodeAddress]channelLayout,
	changes []layoutChange) (map[nodeAddress]channelLayout, error) {
	next := maps.Clone(declared)
	for _, change := range changes {
		next[change.Endpoint.graphAddress()] = change.To
	}
	document := nodeConfig(declaredEndpoints(nodes), next)
	if err := writeDeclaration(document); err != nil {
		return nil, err
	}
	r.declared = document
	r.restartRequested = time.Now()
	for _, change := range changes {
		message := fmt.Sprintf("the channel layout changes from %s to %s; "+
			"PipeWire restarts in its container to apply it", change.From, change.To)
		fmt.Printf("%s: %s\n", change.Endpoint.Name(), message)
		r.control.noteSinks([]string{change.Endpoint.Name()}, reasonLayoutChanged, message)
	}
	return next, nil
}

// layoutStates composes the LayoutApplied state of every declared
// sink. A sink whose change waits for idle reports AwaitingIdle, and
// every sink reports Restarting while the drop-in is newer than the
// PipeWire that runs.
func (r *reconciler) layoutStates(endpoints []alsaEndpoint, declared map[nodeAddress]channelLayout,
	waiting map[string]layoutChange) map[string]layoutState {
	restarting := r.declarationStale != nil && r.declarationStale()
	states := map[string]layoutState{}
	for _, endpoint := range endpoints {
		layout, isDeclared := declared[endpoint.graphAddress()]
		if endpoint.Capture || !isDeclared {
			continue
		}
		state := layoutState{Layout: layout, Applied: true, Reason: layoutReasonApplied,
			Message: "PipeWire runs the sink with " + layout.String()}
		if change, ok := waiting[endpoint.Name()]; ok {
			state = layoutState{Layout: layout, Reason: layoutReasonAwaitingIdle,
				Message: fmt.Sprintf("the layout changes to %s when no stream plays through this machine's PipeWire",
					change.To)}
		} else if restarting {
			state = layoutState{Layout: layout, Reason: layoutReasonRestarting,
				Message: "the kubelet is restarting the PipeWire container to apply " + layout.String()}
		}
		states[endpoint.Name()] = state
	}
	return states
}

// specLayouts reads every named sink's spec.layout. A reconciler with
// no endpoint controller, which is what a test of the slice alone
// builds, reads none.
func (r *reconciler) specLayouts(endpoints []alsaEndpoint) (map[nodeAddress][]string, error) {
	if r.control == nil {
		return nil, nil
	}
	sinks, err := r.control.cachedSinks(endpoints)
	if err != nil {
		return nil, err
	}
	return specLayouts(endpoints, sinks), nil
}

// reportLayout keeps a failure of the layout step to one line for each
// run of passes that finds it, the way driftReported does for the PCM
// devices. An empty line clears it.
func (r *reconciler) reportLayout(line string) {
	if line == r.layoutReport {
		return
	}
	r.layoutReport = line
	if line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

// awaitingRestart reports whether the PipeWire container is restarting
// for a layout change this operator wrote, which is when a graph read
// that fails is expected.
func (r *reconciler) awaitingRestart() bool {
	return !r.restartRequested.IsZero() && time.Since(r.restartRequested) < layoutRestartGrace
}

// reportLayoutWrite reports a declaration that did not write, once
// for each run of passes that meets the same error: one line, and one
// Warning on each Sink whose layout waits for the write. The next pass
// tries the write again, and a write that lands clears the report.
func (r *reconciler) reportLayoutWrite(changes []layoutChange, err error) {
	line := fmt.Sprintf("writing the new layouts: %v", err)
	if line == r.layoutWriteFailure {
		return
	}
	r.layoutWriteFailure = line
	fmt.Fprintln(os.Stderr, line)
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, change.Endpoint.Name())
	}
	r.control.warnSinks(names, reasonLayoutWriteFailed,
		fmt.Sprintf("the operator could not write the new channel layout, and the sink keeps its layout: %v", err))
}

// playing reports whether a link touches any node this operator
// publishes: a card's sink or source, or a speaker. A restart of the
// PipeWire container ends every stream on every one of them, so
// reconcileLayouts writes no new layout while this is true.
func (g pwGraph) playing() bool {
	for _, node := range g.Nodes {
		if g.Linked[node.ID] {
			return true
		}
	}
	for _, sink := range g.Speakers {
		if g.Linked[sink.NodeID] {
			return true
		}
	}
	return false
}
