package main

// Every reason a Display's conditions and Events carry. A reason is
// part of the published interface: a person reads it in `kubectl
// describe display`, and a controller such as media-operator acts on
// it. So the reasons are in one file, and the troubleshooting guide
// names each one.
//
// Each condition transition posts one Event with the condition's own
// reason and message (displayStore.announce). The Events that mark an
// action the operator took, and change no condition, have reasons of
// their own at the end of this file.

import "github.com/liken-sh/liken/kubernetes/conditions"

// The reasons of the Connected condition. PanelAttached is a panel on
// the Display's connector. NoPanel is a connector that serves no EDID
// for this monitor. media-operator reads NoPanel as a screen that is
// away.
const (
	PanelAttachedReason = "PanelAttached"
	NoPanelReason       = "NoPanel"
)

// The reasons of the Responsive condition: the panel answers DDC/CI,
// or it does not. A panel in standby does not answer, so NoDDCReply is
// often an expected state.
const (
	AnswersDDCReason = "AnswersDDC"
	NoDDCReplyReason = "NoDDCReply"
)

// The reasons of the CompositorServing condition. Serving is a
// compositor that answered the probe. Down is a socket that refuses
// the connect or ends under the probe. Hung is a socket that accepts
// and answers nothing.
const (
	CompositorServingReason = "Serving"
	CompositorDownReason    = "Down"
	CompositorHungReason    = "Hung"
)

// The reasons of the PhysicalAddressCurrent condition. ReadFromEDID
// is an address the connector's current EDID serves. Retained is the
// last valid address, kept while the connector serves no EDID for
// this monitor or serves no valid address in it. Ambiguous is a
// monitor that two connectors on this node both serve, with different
// addresses.
const (
	ReadFromEDIDReason = "ReadFromEDID"
	RetainedReason     = "Retained"
	AmbiguousReason    = "Ambiguous"
)

// The reasons of the LayoutResolved condition. LayoutNotFound is a
// name that resolves to nothing, and the screen shows the default
// layout in that state, so the condition is the only report of the
// name that failed. When the condition is met, the screen shows the
// Layout it names (LayoutFound), or it names none and shows the
// default (DefaultLayout). They are two states of one condition,
// because a reader asking why a screen is arranged as it is needs to
// know which.
const (
	LayoutNotFoundReason = "LayoutNotFound"
	LayoutFoundReason    = "LayoutFound"
	DefaultLayoutReason  = "DefaultLayout"
)

// The reasons of the Events that mark an action, not a condition.
//
// Captured is a capture of the screen through the API. The Event is
// the record of who looked at a screen, and when.
//
// CompositorKilled is a compositor that answered nothing for
// compositorHungLimit, which the operator ended so the kubelet starts
// it again. Every screen on the card blanks, so the Event goes on every
// Display of the node.
//
// ModeChanged is a mode write that restarts the compositor. Every
// screen on the card blanks while the compositor restarts, and the
// Event names the screen whose mode caused it.
//
// WriteUnconfirmed is a write the device did not confirm, recorded in
// status.unconfirmed. The operator does not make the write again until
// spec changes.
//
// PanelStandbyFailed is a panel that did not take the standby that
// follows the release of its last claim. The operator does not try
// again, so the panel stays on until a person or a claim turns it off.
const (
	CapturedReason           = "Captured"
	CompositorKilledReason   = "CompositorKilled"
	ModeChangedReason        = "ModeChanged"
	WriteUnconfirmedReason   = "WriteUnconfirmed"
	PanelStandbyFailedReason = "PanelStandbyFailed"
)

// badStatus answers the status of a condition that makes its Event a
// Warning, or "" when no status of it needs a person. Each condition
// here is False both in a state a person causes and in a fault, so the
// reason decides. A missing Layout, a compositor that is down or hung,
// and two connectors that disagree on an address need a person. A
// panel unplugged, a panel in standby, and an address kept while a
// receiver is in standby do not.
func badStatus(c DisplayCondition) conditions.Status {
	switch c.Reason {
	case LayoutNotFoundReason, CompositorDownReason, CompositorHungReason, AmbiguousReason:
		return conditions.False
	}
	return ""
}
