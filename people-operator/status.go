package main

// The operator writes a Person's status after each read of its
// spec.avatar. When a read fails, status.thumbnail keeps the last good
// picture and AvatarReady reports the failure. A Person with
// no picture gets its initials.

import (
	"reflect"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// outcome is the result of reading one Person's spec.avatar.
type outcome struct {
	// source is the spec.avatar that was read, and checked is the
	// check-avatar annotation value at that time.
	source, checked string

	// initials is true when spec.avatar is empty.
	initials bool

	// reading is the result of a successful read, and reason is the
	// AvatarReady reason to report for it.
	reading reading
	reason  string

	// err is the error of a failed read. reasonOf takes the
	// AvatarReady reason from it.
	err error
}

// needsRead reports whether a Person's status is out of date with its
// spec: it has no thumbnail, it was read from a different source or
// for a different check-avatar value, or its condition observed an
// earlier generation. The generation check redraws the initials after
// the person changes their name.
func needsRead(p *person) bool {
	return p.Status.Thumbnail == "" ||
		p.Spec.Avatar != p.Status.Avatar.Source ||
		p.checkRequest() != p.Status.Avatar.Checked ||
		p.Metadata.Generation != p.Status.ready().ObservedGeneration
}

// knownVersion returns the version of the picture in status.thumbnail
// when it came from the Person's current spec.avatar, and the zero
// version otherwise. A source reports a picture unchanged only when it
// matches a version that is not zero. So a source never reports
// unchanged for a thumbnail from another source or for initials.
func knownVersion(p *person) pictureVersion {
	if !hasPicture(p) || p.Status.Avatar.Source != p.Spec.Avatar {
		return pictureVersion{}
	}
	return p.Status.Avatar.version()
}

// hasPicture reports whether status.thumbnail is a picture from a
// source and not drawn initials.
func hasPicture(p *person) bool {
	return p.Status.Thumbnail != "" && !isInitials(p.Status.Thumbnail)
}

// composeStatus returns the status that an outcome gives a Person, and
// what changed. now becomes the condition's lastTransitionTime when its
// status changes.
func composeStatus(p *person, out outcome, now time.Time) (personStatus, change) {
	next := p.Status
	next.Avatar = avatarState{Source: out.source, Checked: out.checked}
	ready := condition{Type: avatarReady, Status: conditions.True, ObservedGeneration: p.Metadata.Generation}
	var changed change

	switch {
	case out.err != nil:
		ready.Status, ready.Reason = conditions.False, reasonOf(out.err)
		if hasPicture(p) {
			// Keep the version only when the picture came from this
			// source. A version from another source would make the next
			// read of this source report unchanged.
			if p.Status.Avatar.Source == out.source {
				next.Avatar = withVersion(next.Avatar, p.Status.Avatar.version())
			}
			ready.Message = "status.thumbnail keeps the last good picture: " + out.err.Error()
		} else {
			next.Thumbnail = drawInitials(p)
			ready.Message = "status.thumbnail shows the initials: " + out.err.Error()
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

	// Clone the conditions so that composing does not modify the
	// caller's copy of the Person. conditions.Set changes
	// lastTransitionTime only when the status changes, so recomputing
	// an unchanged status gives an equal result.
	next.Conditions = slices.Clone(p.Status.Conditions)
	ready.LastTransitionTime = now.UTC().Truncate(time.Second)
	changed.transitioned = conditions.Set(&next.Conditions, ready)
	return next, changed
}

func withVersion(state avatarState, version pictureVersion) avatarState {
	state.ETag, state.LastModified, state.Size = version.ETag, version.LastModified, version.Size
	return state
}

func statusChanged(current, next personStatus) bool {
	return !reflect.DeepEqual(current, next)
}
