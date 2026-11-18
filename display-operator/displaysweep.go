package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// sweep writes the CompositorServing condition from the compositor
// probe on every Display of this node. Each screen's report writes only
// the Displays of connected monitors, and without the sweep the Display
// of a disconnected monitor would keep a stale status.
//
// For a disconnected monitor, sweep also clears surfaces, layout, and
// arrangers, because the compositor destroys the output and the layout
// module hides each window that was on it. A Display whose status
// already matches is not written.
func (p *placementPass) sweep(live compositorLiveness) error {
	return p.settleEach(func(name string, published DisplayStatus) DisplayStatus {
		status := published
		if _, connected := slices.BinarySearch(p.connectedDisplays, name); !connected {
			status.Surfaces = nil
			status.Layout = nil
			status.Arrangers = nil
		}
		status.Conditions = setCondition(status.Conditions, p.serving(live))
		return status
	})
}

// connectedDisplayNames returns the sorted Display names of the connected
// monitors.
func connectedDisplayNames(outputs []Output) []string {
	var names []string
	for _, output := range outputs {
		if output.Connected {
			names = append(names, monitorID(output.Monitor))
		}
	}
	slices.Sort(names)
	return names
}

// settleEach writes the status that compose returns on every Display
// whose status.node is this node. On a write conflict it calls compose
// again on the fresh copy, and skips the Display if its status.node has
// changed to another node.
func (p *placementPass) settleEach(compose func(name string, published DisplayStatus) DisplayStatus) error {
	displays, err := p.displays.list()
	if err != nil {
		return err
	}
	var failures []error
	for _, display := range displays {
		if display.Status.Node != p.node {
			continue
		}
		name := display.Metadata.Name
		ours := func(published DisplayStatus) (DisplayStatus, bool) {
			return compose(name, published), published.Node == p.node
		}
		if err := p.displays.settleStatus(&display, ours); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
			failures = append(failures, fmt.Errorf("settling the status of Display %s: %w", display.Metadata.Name, err))
		}
	}
	return errors.Join(failures...)
}
