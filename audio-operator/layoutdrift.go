package main

// A sink's channel layout changes only when PipeWire restarts, because
// PipeWire reads the drop-in that declares its nodes only while it
// loads its configuration, and WirePlumber reads a node's
// formats only when it configures the node. After the operator writes a
// new drop-in, runPipewire, the first process of the PipeWire
// container, finds the drop-in newer than PipeWire's socket and
// restarts PipeWire inside the container (declarationloaded.go,
// restarts.go). WirePlumber exits when PipeWire goes away, and
// runWireplumber starts it again, so it configures every node again.
// The pod and the operator keep running.
//
// The selected layout differs from the declared one when spec.layout
// changed, or when an HDMI output reports an ELD whose layout or
// encoded formats differ from the declared ones. An ELD that disappears
// keeps the declared layout (selectLayout), so switching a television
// off changes nothing.
//
// One PipeWire serves every card and speaker on the machine, so the
// restart ends every stream, and the operator writes a new drop-in only
// while no link touches a node that it publishes.

import (
	"fmt"
	"maps"
	"os"
	"time"
)

// layoutRestartGrace is how long after a drop-in write a failed graph
// read does not count toward maxFailedGraphReads, because PipeWire is
// restarting. After this time, a failed read counts again.
const layoutRestartGrace = time.Minute

// The reasons of a Sink's LayoutApplied condition.
const (
	layoutReasonApplied      = "Applied"
	layoutReasonAwaitingIdle = "AwaitingIdle"
	layoutReasonRestarting   = "Restarting"
)

// layoutState is one sink's declared layout and its LayoutApplied
// condition.
type layoutState struct {
	Layout  channelLayout
	Applied bool
	Reason  string
	Message string
}

type layoutChange struct {
	Endpoint alsaEndpoint
	From, To channelLayout
}

// planLayouts returns the sinks whose selected layout differs from the
// declared one. It skips an endpoint with no declared node, which is a
// PCM device that appeared after the pod started. The declare init
// container writes the node list, so only a new pod can add that
// device, and reconcile logs the change in the card's PCM devices once
// (driftReported).
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

// reconcileLayouts writes the layout changes that this pass finds and
// returns the layoutState of every declared sink, keyed by the
// endpoint's Name(). It returns nil when the drop-in does not
// parse. It writes nothing when it cannot read the Sinks, because a
// Sink that failed to read has no spec.layout, and the change would
// revert a layout that the Sink sets.
func (r *reconciler) reconcileLayouts(endpoints []alsaEndpoint, graph pwGraph) map[string]layoutState {
	nodes, err := parseDeclaration(r.declared)
	if err != nil {
		r.reportLayout(fmt.Sprintf("reading the declaration PipeWire loaded: %v", err))
		return nil
	}
	declared := declaredLayouts(nodes)
	specs, err := r.specLayouts(endpoints)
	if err != nil {
		r.reportLayout(fmt.Sprintf("the layouts stay as declared: %v", err))
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

// applyLayouts writes every change in one drop-in, so PipeWire restarts
// once, then logs each change and posts an Event on its Sink. It
// returns the layouts of the new drop-in.
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
		what := fmt.Sprintf("the channel layout from %s to %s, "+
			"so PipeWire restarts in its container to apply it", change.From, change.To)
		fmt.Printf("%s: changed %s\n", change.Endpoint.Name(), what)
		r.control.noteSinks([]string{change.Endpoint.Name()}, reasonLayoutChanged, "Changed "+what)
	}
	return next, nil
}

// layoutStates returns the layoutState of every declared sink. A sink
// in waiting reports AwaitingIdle. Every other sink reports Restarting
// while the drop-in is newer than the running PipeWire, and Applied
// after that.
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
				Message: fmt.Sprintf("The layout changes to %s when no stream plays through this machine's PipeWire",
					change.To)}
		} else if restarting {
			state = layoutState{Layout: layout, Reason: layoutReasonRestarting,
				Message: "PipeWire is restarting in its container to apply " + layout.String()}
		}
		states[endpoint.Name()] = state
	}
	return states
}

// specLayouts returns the spec.layout of each Sink, or none when
// control is nil, as in a test of the ResourceSlice alone.
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

// reportLayout writes line to stderr when it differs from the last line,
// so a failure that lasts is logged once and not on every pass. An
// empty line clears the last line and writes nothing.
func (r *reconciler) reportLayout(line string) {
	if line == r.layoutReport {
		return
	}
	r.layoutReport = line
	if line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

func (r *reconciler) awaitingRestart() bool {
	return !r.restartRequested.IsZero() && time.Since(r.restartRequested) < layoutRestartGrace
}

// reportLayoutWrite logs a failed drop-in write and posts a Warning
// Event on each Sink that the write would change. Each pass retries the
// write, and the same error on a later pass is not reported again until
// a write succeeds.
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
		fmt.Sprintf("The sink keeps its channel layout: writing the new layout: %v", err))
}

// playing reports whether a link touches a node that this operator
// publishes: a card's sink or source, or a Bluetooth speaker.
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
