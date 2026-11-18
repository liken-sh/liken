package main

// The Events about a Display. A Display is cluster-scoped, so the
// recorder writes its Events in the default namespace. Each Event
// carries the Display's UID, because `kubectl describe display` selects
// Events by the involved object's UID and shows none without it.

import (
	"fmt"
	"os"
	"slices"
	"sync/atomic"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

func displayReference(display *Display) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: DisplayAPIVersion,
		Kind:       "Display",
		Name:       display.Metadata.Name,
		UID:        display.Metadata.UID,
	}
}

// announce posts the Events for a status write that succeeded: one for
// each condition whose status or reason changed, and a Warning for each
// new entry of status.unconfirmed.
func (s *displayStore) announce(display *Display, before DisplayStatus) {
	if s.recorder == nil {
		return
	}
	object := displayReference(display)
	for _, next := range display.Status.Conditions {
		previous, found := conditions.Find(before.Conditions, next.Type)
		if found && previous.Status == next.Status && previous.Reason == next.Reason {
			continue
		}
		s.recorder.Transition(object, next, badStatus(next))
	}
	for _, entry := range display.Status.Unconfirmed {
		if slices.Contains(before.Unconfirmed, entry) {
			continue
		}
		s.recorder.Warning(object, WriteUnconfirmedReason, unconfirmedMessage(entry))
	}
}

func unconfirmedMessage(entry DisplayUnconfirmed) string {
	message := fmt.Sprintf("The device did not confirm the write of %s %s", entry.Control, entry.Value)
	if entry.Readback != "" {
		message += ", and it reports " + entry.Readback
	}
	if entry.Message != "" {
		message += ": " + entry.Message
	}
	return message + ". The operator does not write the value again until spec changes"
}

// screenNotices posts Events about actions that the operator takes on
// this node's screens, such as restarting the compositor. The code that
// acts has a connector and no Display, so screenNotices finds the
// Display whose status has this node and that connector.
//
// main creates the DRA plugin before it opens the Display watch, and
// resumeReleases can start a standby that fails before the store
// exists. So main sets the store later, through an atomic pointer.
// Until then, and on a nil *screenNotices, post does nothing.
type screenNotices struct {
	node     string
	displays atomic.Pointer[displayStore]
}

// post sends one Event about the Display of this node that has this
// connector, or about every Display of this node when connector is
// empty.
func (n *screenNotices) post(connector, eventType, reason, message string) {
	if n == nil {
		return
	}
	store := n.displays.Load()
	if store == nil || store.recorder == nil {
		return
	}
	displays, err := store.list()
	if err != nil {
		fmt.Fprintf(os.Stderr, "posting the %s Event: reading the Displays: %v\n", reason, err)
		return
	}
	for i := range displays {
		display := &displays[i]
		if display.Status.Node != n.node || (connector != "" && display.Status.Connector != connector) {
			continue
		}
		object := displayReference(display)
		if eventType == events.TypeWarning {
			store.recorder.Warning(object, reason, message)
		} else {
			store.recorder.Normal(object, reason, message)
		}
	}
}
