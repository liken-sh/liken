package main

// The status answers a Person's spec.avatar: the thumbnail, a record of
// what it answers, and the AvatarReady condition. The rules for a read
// that failed keep a good picture: the last photograph stays, with the
// reason on the condition, and a Person that never had a good picture
// shows its initials.

import (
	"reflect"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// outcome is the answer to one Person's spec.avatar.
type outcome struct {
	// source is the spec.avatar the outcome answers, and checked is the
	// check-avatar annotation value it answers.
	source, checked string

	// initials is true when the Person names no picture.
	initials bool

	// reading is a read that worked, and reason is its AvatarReady
	// reason.
	reading reading
	reason  string

	// err is a read that failed. It carries its reason (sourceError).
	err error
}

// needsRead reports whether a Person's status does not answer its
// spec: it has no thumbnail, it answers another source or another
// check request, or its condition answers an earlier generation of
// the spec. The last rule redraws the initials after a person changes
// their name.
func needsRead(p *person) bool {
	return p.Status.Thumbnail == "" ||
		p.Spec.Avatar != p.Status.Avatar.Source ||
		p.checkRequest() != p.Status.Avatar.Checked ||
		p.Metadata.Generation != p.Status.ready().ObservedGeneration
}

// knownVersion answers the version of the picture that the status
// holds for the Person's current source, and the zero version when the
// thumbnail is not a picture from that source. A read with the zero
// version never answers unchanged, so a thumbnail that answers
// unchanged is always a picture the operator already holds.
func knownVersion(p *person) pictureVersion {
	if !heldPicture(p) || p.Status.Avatar.Source != p.Spec.Avatar {
		return pictureVersion{}
	}
	return p.Status.Avatar.version()
}

// heldPicture reports whether the status holds a picture from a source,
// and not initials.
func heldPicture(p *person) bool {
	return p.Status.Thumbnail != "" && !isInitials(p.Status.Thumbnail)
}

// composeStatus answers the status that an outcome gives a Person, and
// what it changed that posts an Event. now stamps the condition when
// its status changes.
func composeStatus(p *person, out outcome, now time.Time) (personStatus, change) {
	next := p.Status
	next.Avatar = avatarState{Source: out.source, Checked: out.checked}
	ready := condition{Type: avatarReady, Status: conditions.True, ObservedGeneration: p.Metadata.Generation}
	var changed change

	switch {
	case out.err != nil:
		ready.Status, ready.Reason, ready.Message = conditions.False, reasonOf(out.err), out.err.Error()
		if heldPicture(p) {
			// The version stays only with a picture from the same
			// source. A version from another source would make the
			// next read of this one answer unchanged.
			if p.Status.Avatar.Source == out.source {
				next.Avatar = withVersion(next.Avatar, p.Status.Avatar.version())
			}
			ready.Message += "; the last good picture stays"
		} else {
			next.Thumbnail = drawInitials(p)
		}
	case out.initials:
		next.Thumbnail, ready.Reason = drawInitials(p), reasonInitials
	case out.reading.unchanged:
		next.Avatar = withVersion(next.Avatar, out.reading.version)
		ready.Reason = out.reason
	default:
		next.Thumbnail = out.reading.thumbnail
		next.Avatar = withVersion(next.Avatar, out.reading.version)
		ready.Reason = out.reason
		changed.newPicture = next.Thumbnail != p.Status.Thumbnail
	}

	// The list is cloned, because the held status is the API server's
	// copy until the write lands. Set moves lastTransitionTime only
	// when the status changes, so a write that changes nothing else
	// changes nothing.
	next.Conditions = slices.Clone(p.Status.Conditions)
	ready.LastTransitionTime = now.UTC().Truncate(time.Second)
	changed.transitioned = conditions.Set(&next.Conditions, ready)
	return next, changed
}

func withVersion(state avatarState, version pictureVersion) avatarState {
	state.ETag, state.LastModified, state.Size = version.ETag, version.LastModified, version.Size
	return state
}

// statusChanged reports whether a composed status differs from the one
// the API server holds, so the operator writes only a change.
func statusChanged(held, next personStatus) bool {
	return !reflect.DeepEqual(held, next)
}
