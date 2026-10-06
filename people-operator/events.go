package main

// The operator posts a Kubernetes Event about a Person for each change
// of its AvatarReady condition, with the condition's reason and
// message, and for each new picture that leaves the condition as it
// was. The condition answers what is true now. The Events answer what
// happened in the last hour: a source that failed overnight and then
// answered again leaves no trace in the condition. A Person is
// cluster-scoped, so its Events are in the namespace default.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// component names the operator in each Event's source.
const component = "people-operator"

// The reasons AvatarReady reports. Each one is also the reason of the
// Event that a change to it posts. The first four have the status True.
// The last four have the status False, and their Events are Warnings.
const (
	reasonFetched           = "Fetched"
	reasonInline            = "Inline"
	reasonBaked             = "Baked"
	reasonInitials          = "Initials"
	reasonFetchFailed       = "FetchFailed"
	reasonBakeFailed        = "BakeFailed"
	reasonDecodeFailed      = "DecodeFailed"
	reasonUnsupportedScheme = "UnsupportedScheme"
)

// reasonAvatarUpdated is the Event of a new picture that changes no
// condition: the source answered a new copy under the same reason, or
// spec.avatar names a new source of the same kind.
const reasonAvatarUpdated = "AvatarUpdated"

// change is what one status write changed that a person reads as an
// Event.
type change struct {
	// transitioned is true when AvatarReady is new, or its status or
	// its reason changed.
	transitioned bool

	// newPicture is true when the thumbnail holds a picture the status
	// did not hold before.
	newPicture bool
}

// post posts the Events of a change after its status write landed. A
// write that the API server refused posts nothing, and the next pass
// finds the same change again.
func (o *operator) post(p *person, c change) {
	about := events.ObjectReference{
		APIVersion: personAPIVersion,
		Kind:       personKind,
		Name:       p.Metadata.Name,
		UID:        p.Metadata.UID,
	}
	switch {
	case c.transitioned:
		o.recorder.Transition(about, p.Status.ready(), conditions.False)
	case c.newPicture:
		o.recorder.Normal(about, reasonAvatarUpdated, "the source answered a new picture, and the thumbnail shows it")
	}
}
