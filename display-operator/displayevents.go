package main

// The Events about a Display. A Display is cluster-scoped, so the
// recorder writes its Events in the namespace default, and `kubectl
// describe display` finds them there. Each Event names the Display's
// UID, because kubectl describe searches a resource's Events by UID
// and finds none written without it.

import (
	"fmt"
	"os"
	"slices"
	"sync/atomic"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// displayReference names one Display as an Event's involved object.
func displayReference(display *Display) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: DisplayAPIVersion,
		Kind:       "Display",
		Name:       display.Metadata.Name,
		UID:        display.Metadata.UID,
	}
}

// announce posts the Events of a status write that landed: one for
// each condition that transitioned from before, and one Warning for
// each write the device did not confirm that the status now records
// for the first time.
func (s *displayStore) announce(display *Display, before DisplayStatus) {
	if s.recorder == nil {
		return
	}
	object := displayReference(display)
	for _, next := range display.Status.Conditions {
		held, found := conditions.Find(before.Conditions, next.Type)
		if found && held.Status == next.Status && held.Reason == next.Reason {
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

// unconfirmedMessage states one unconfirmed write: what the operator
// wrote, what the device held after it, and the failure in the words
// of the party that reported it.
func unconfirmedMessage(entry DisplayUnconfirmed) string {
	message := fmt.Sprintf("the operator wrote %s %s and the device did not confirm it", entry.Control, entry.Value)
	if entry.Readback != "" {
		message += "; it reads back " + entry.Readback
	}
	if entry.Message != "" {
		message += ": " + entry.Message
	}
	return message + ". The operator does not write it again until spec changes"
}

// screenNotices posts the Events of an action on the screens of this
// node, such as a compositor restart, from the code that takes the
// action. That code knows a connector, not a Display, so the notices
// find the Display that names this node and the connector in its
// status.
//
// The DRA plugin is built before the Display watch opens, and a
// standby that a restarted operator resumes can fire before the store
// is set, so the store is an atomic pointer. A notice before the store
// is set, and a notice on a nil screenNotices, posts nothing.
type screenNotices struct {
	node     string
	displays atomic.Pointer[displayStore]
}

// post puts one Event on the Display of a connector of this node, or
// on every Display of this node when connector is empty.
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
