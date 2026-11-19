package main

// The operator posts a Kubernetes Event about a Person when its
// AvatarReady condition transitions, and when a new picture arrives
// without a transition. The condition shows the current state. The
// Events show what happened recently: a source that failed overnight
// and then recovered leaves nothing in the condition. A Person is
// cluster-scoped, so its Events are in the `default` namespace.

import (
	"fmt"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
)

// component is the source of each Event.
const component = "people-operator"

// The reasons of the AvatarReady condition, which are also the reasons
// of the Events that its transitions post. Fetched, Inline, Baked, and
// Initials go with status True. The others go with status False, and
// their Events are Warnings.
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

// reasonAvatarUpdated is the reason of the Event for a new picture that
// leaves the condition's status and reason unchanged. It happens when
// the same source returns a different picture, or when spec.avatar
// points to a different source that succeeds with the same reason.
const reasonAvatarUpdated = "AvatarUpdated"

// reasonPodRecreated is the reason of the Warning for a baker pod that
// the operator created again, because the kubelet ended the earlier
// pod before the bake command wrote its result.
const reasonPodRecreated = "PodRecreated"

// change is what a status write changed, which decides the Event.
type change struct {
	// transitioned is true when AvatarReady is new, or its status or
	// reason changed.
	transitioned bool

	// newPicture is true when a read replaced status.thumbnail with a
	// different picture.
	newPicture bool
}

// post is called only after the status write succeeds. When the API
// server rejects the write, the next pass computes the same change and
// posts its Event then.
func (o *operator) post(p *person, c change) {
	about := personRef(p)
	switch {
	case c.transitioned:
		o.recorder.Transition(about, p.Status.ready(), conditions.False)
	case c.newPicture:
		o.recorder.Normal(about, reasonAvatarUpdated, "Replaced status.thumbnail with a new picture from spec.avatar")
	}
}

// postRecreated posts the Warning for the next baker pod that the
// operator created in place of a pod that the kubelet ended. The
// message gives the kubelet's reason and message, so a person can tell
// an eviction from a node shutdown.
func (o *operator) postRecreated(p *person, ended pod) {
	cause := "the condition DisruptionTarget"
	if ended.Status.Reason != "" {
		cause = "the reason " + ended.Status.Reason
	}
	message := fmt.Sprintf("Created the baker pod %s/%s in place of %s, which the kubelet ended with %s before it wrote a result",
		ended.Metadata.Namespace, tryPodName(p.Metadata.Name, ended.tries()+1), ended.Metadata.Name, cause)
	if ended.Status.Message != "" {
		message += ": " + strings.TrimSuffix(ended.Status.Message, ".")
	}
	o.recorder.Warning(personRef(p), reasonPodRecreated, message)
}

func personRef(p *person) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: personAPIVersion,
		Kind:       personKind,
		Name:       p.Metadata.Name,
		UID:        p.Metadata.UID,
	}
}
