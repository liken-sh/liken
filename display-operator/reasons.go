package main

// The reasons of a Display's conditions and Events. They are part of
// the published interface: `kubectl describe display` shows them, and
// media-operator reads them. They are in one file so that the
// troubleshooting guide can list each one.
//
// displayStore.announce posts one Event for each condition transition,
// with the condition's reason and message. The reasons at the end of
// this file are for Events of actions that change no condition.

import "github.com/liken-sh/liken/kubernetes/conditions"

// Reasons of the Connected condition. NoPanel means the connector
// serves no EDID of this Display's monitor. media-operator reports it
// on a Player as PanelAway.
const (
	PanelAttachedReason = "PanelAttached"
	NoPanelReason       = "NoPanel"
)

// Reasons of the Responsive condition, which is True when the panel
// replies to DDC/CI. A panel in standby does not reply, so NoDDCReply
// is often expected.
const (
	AnswersDDCReason = "AnswersDDC"
	NoDDCReplyReason = "NoDDCReply"
)

// Reasons of the CompositorServing condition. Down means the socket
// refused the probe's connection or closed during the probe. Hung
// means the socket accepted the connection and the compositor never
// replied.
const (
	CompositorServingReason = "Serving"
	CompositorDownReason    = "Down"
	CompositorHungReason    = "Hung"
)

// Reasons of the PhysicalAddressCurrent condition. Retained means
// status.physicalAddress keeps its last valid value, because the
// connector serves no EDID of this monitor or no valid address in it.
// Ambiguous means two connectors on this node serve the same monitor
// with different addresses.
const (
	ReadFromEDIDReason = "ReadFromEDID"
	RetainedReason     = "Retained"
	AmbiguousReason    = "Ambiguous"
)

// Reasons of the LayoutResolved condition. It is False with
// LayoutNotFound when no Layout has the name that the Display gives.
// The screen then shows the default layout, so this condition is the
// only report of the wrong name. It is True with LayoutFound when the
// screen shows the named Layout, and True with DefaultLayout when the
// Display names no Layout.
const (
	LayoutNotFoundReason = "LayoutNotFound"
	LayoutFoundReason    = "LayoutFound"
	DefaultLayoutReason  = "DefaultLayout"
)

// Reasons of the Events of actions.
//
// Captured: someone captured the screen through the API. The Event
// records who captured it and when.
//
// CompositorKilled: the compositor did not reply for
// compositorHungLimit, so the operator sent it SIGKILL, and its
// container starts it again. Every screen on the card goes blank, so
// the Event is posted on every Display of the node.
//
// ModeChanged: a mode write restarts the compositor, and every screen
// on the card goes blank. The Event is on the Display whose mode
// changed.
//
// WriteUnconfirmed: the device did not confirm a write, and
// status.unconfirmed records it. The operator does not repeat the
// write until spec changes.
//
// PanelStandbyFailed: the panel did not go to standby after its last
// claim was released. The operator does not retry, so the panel stays
// on until a person or a claim turns it off.
//
// ArrangerGranted: the compositor made a claim's client the arranger
// of that claim, and the client places the claim's surfaces inside the
// claim's region. ArrangerLost: the arranger's client disconnected
// while a pod of the claim still runs, which is how a crashed client
// looks, and the claim's surfaces fill the region again.
// ArrangerReleased: the arranger gave up the region, or its client
// disconnected after the claim's pods stopped.
const (
	CapturedReason           = "Captured"
	CompositorKilledReason   = "CompositorKilled"
	ModeChangedReason        = "ModeChanged"
	WriteUnconfirmedReason   = "WriteUnconfirmed"
	PanelStandbyFailedReason = "PanelStandbyFailed"
	ArrangerGrantedReason    = "ArrangerGranted"
	ArrangerLostReason       = "ArrangerLost"
	ArrangerReleasedReason   = "ArrangerReleased"
)

// badStatus returns the status at which the Event of c is a Warning,
// or "" when its Event is always Normal. A condition can be False because of
// a fault or because of something a person did, so the reason decides.
// A missing Layout, a compositor that is down or hung, and two
// connectors that disagree on an address need a person. An unplugged
// panel, a panel in standby, and a retained address do not.
func badStatus(c DisplayCondition) conditions.Status {
	switch c.Reason {
	case LayoutNotFoundReason, CompositorDownReason, CompositorHungReason, AmbiguousReason:
		return conditions.False
	}
	return ""
}
