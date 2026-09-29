package main

// The node-wide report of the placement pass: the writes that reach
// every Display of this node, and not only the Displays of the screens
// the pass draws. A Display whose monitor is off the wire has no
// screen, so no screen's report ever writes it, and these writes are
// what keep its status true.

import (
	"errors"
	"fmt"
	"slices"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// sweep writes the compositor's answer on every Display of this node.
// A screen's report covers a Display whose monitor is on the wire, and
// this covers the rest: a monitor that left is still on this node's
// card, and the condition reports the compositor that serves that
// card.
//
// A Display whose monitor is off the wire also loses its windows and
// its arrangement. The compositor destroyed that output, and the
// module hid each window that was on it, so no window is on that
// screen and no region is drawn. A Display whose monitor is on the
// wire keeps both, because its screen's report writes them, and a
// screen the pass leaves as it is must keep what it shows.
//
// The write happens only on a Display whose status differs, so a
// sweep over current Displays writes nothing.
func (p *placementPass) sweep(live compositorLiveness) error {
	return p.settleEach(func(name string, published DisplayStatus) DisplayStatus {
		status := published
		if _, wired := slices.BinarySearch(p.wired, name); !wired {
			status.Surfaces = nil
			status.Layout = nil
		}
		status.Conditions = setCondition(status.Conditions, p.serving(live))
		return status
	})
}

// wiredDisplays names the Display of each monitor on the wire, sorted,
// the way the Display controller names it.
func wiredDisplays(outputs []Output) []string {
	var names []string
	for _, output := range outputs {
		if output.Connected {
			names = append(names, monitorID(output.Monitor))
		}
	}
	slices.Sort(names)
	return names
}

// settleEach writes compose's status on every Display of this node.
// A retry after a conflict composes again from the fresh copy, and
// leaves a Display that another node took since.
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
			failures = append(failures, fmt.Errorf("%s: %w", display.Metadata.Name, err))
		}
	}
	return errors.Join(failures...)
}
