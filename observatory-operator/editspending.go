package main

// A session acts on its record, not on the specs (records.go), so an
// edit of a recorded fact during a session changes nothing that runs
// until the next session. A device that an edit moves says so in its
// ParentFound condition (standing.go). The Reservation's
// EditsPending condition says the same of the facts that the session
// records for the reservation itself: spec.outbox, and the merged FITS
// keywords of each imaging camera, which the Observatory, the
// Telescope, the Camera, and the Reservation declare. It is True, with
// the reason EditPending, when an edit waits, and False when the record
// holds what PowerOn would record from the specs now. Its message
// names each fact that changes, with the value it has now and the value
// it takes at the next session.
//
// The condition changes only when a spec changes, so the runner judges
// it where it reacts to an edit (observe), and when it records the
// session (recordSession, recordAgain), and not on each write of the
// status. A reservation has the condition only while it has a session:
// from the PowerOn step's record until it is Released.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The reasons of the EditsPending condition.
const (
	reasonNoEditPending = "NoEditPending"
	reasonEditPending   = "EditPending"
)

// judgeSession sets the runner's EditsPending condition from the
// session record and the specs in t, and reports whether it changed.
func (r *runner) judgeSession(t *tree) bool {
	next := r.sessionCondition(t)
	if sameState(next, r.session) {
		return false
	}
	r.session = next
	return true
}

// sameState reports whether two conditions, either of which can be
// absent, hold the same status, reason, and message. The transition
// time is left out, because a condition composed again carries the
// present time, and the write keeps the stored one (mergeConditions).
func sameState(a, b *observatory.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}

// sessionCondition answers the EditsPending condition, or nil when
// the reservation has no session. The telescope's part of the record
// holds the outbox and the keywords, so a record with no devices of
// the telescope has nothing to compare yet.
func (r *runner) sessionCondition(t *tree) *observatory.Condition {
	s := r.status.Session
	if s == nil || len(s.Devices) == 0 || r.status.Phase == observatory.ReservationReleased {
		return nil
	}
	pending := outboxPending(s.Outbox, r.res.Spec.Outbox)
	ref := serverRef{observatory.TelescopeKind, r.res.Spec.Telescope}
	pending = append(pending, keywordsPending(t, ref, s, r.res.Spec.FITSKeywords)...)
	if len(pending) == 0 {
		c := condition(observatory.ConditionEditsPending, observatory.ConditionFalse, reasonNoEditPending, "No edit waits for the next session")
		return &c
	}
	for i := range pending {
		pending[i] += " at the next session"
	}
	c := condition(observatory.ConditionEditsPending, observatory.ConditionTrue, reasonEditPending, strings.Join(pending, "; "))
	return &c
}

// outboxPending names a change of the outbox's claim.
func outboxPending(recorded, spec *observatory.Outbox) []string {
	claim := func(o *observatory.Outbox) string {
		if o == nil {
			return "none"
		}
		return o.ClaimName
	}
	if was, next := claim(recorded), claim(spec); was != next {
		return []string{fmt.Sprintf("spec.outbox.claimName changes from %s to %s", was, next)}
	}
	return nil
}

// keywordsPending names each change of the merged FITS keywords of the
// session's imaging cameras. It merges the levels over the devices and
// the guide roles that the session recorded, because a device that an
// edit adds or moves waits for the next session on its own
// (standing.go), and so does a Guider (guidersOf). A camera that a
// person deleted during the session left it, and has nothing to
// compare.
func keywordsPending(t *tree, ref serverRef, s *observatory.Session, reservation []observatory.FITSKeyword) []string {
	devices := t.devicesOn(ref)
	next := t.fitsKeywordsOf(ref, s.Observatory, devices, reservation)
	present := map[string]bool{}
	for _, d := range devices {
		present[d.key()] = true
	}
	cameras := map[string]bool{}
	for key := range next {
		cameras[key] = true
	}
	for key := range s.FITSKeywords {
		cameras[key] = true
	}
	var out []string
	for _, key := range slices.Sorted(maps.Keys(cameras)) {
		if present[key] {
			out = append(out, keywordChanges(strings.Replace(key, "/", " ", 1), s.FITSKeywords[key], next[key])...)
		}
	}
	return out
}

// keywordChanges names each keyword of one camera whose value or
// comment changes, in the order of the next list and then of the
// keywords that it drops. A change of order alone changes the header,
// because the driver writes the keywords in the order it receives
// them, so it is named too.
func keywordChanges(camera string, recorded, next []observatory.FITSKeyword) []string {
	quoted := func(k observatory.FITSKeyword, ok bool, field func(observatory.FITSKeyword) string) string {
		if !ok {
			return "none"
		}
		return fmt.Sprintf("%q", field(k))
	}
	value := func(k observatory.FITSKeyword) string { return k.Value }
	comment := func(k observatory.FITSKeyword) string { return k.Comment }
	find := func(list []observatory.FITSKeyword, name string) (observatory.FITSKeyword, bool) {
		i := slices.IndexFunc(list, func(k observatory.FITSKeyword) bool { return k.Name == name })
		if i < 0 {
			return observatory.FITSKeyword{}, false
		}
		return list[i], true
	}
	var names []string
	for _, k := range append(slices.Clone(next), recorded...) {
		if !slices.Contains(names, k.Name) {
			names = append(names, k.Name)
		}
	}
	var out []string
	for _, name := range names {
		was, before := find(recorded, name)
		now, after := find(next, name)
		switch {
		case before != after || was.Value != now.Value:
			out = append(out, fmt.Sprintf("FITS keyword %s of %s changes from %s to %s", name, camera, quoted(was, before, value), quoted(now, after, value)))
		case was.Comment != now.Comment:
			out = append(out, fmt.Sprintf("the comment of FITS keyword %s of %s changes from %s to %s", name, camera, quoted(was, before, comment), quoted(now, after, comment)))
		}
	}
	if len(out) == 0 && !slices.Equal(recorded, next) {
		out = append(out, "the order of the FITS keywords of "+camera+" changes")
	}
	return out
}

// storedSession answers the EditsPending condition that a status
// holds, or nil, so a new runner starts from what the last one wrote
// and an operator restart posts no Event.
func storedSession(status observatory.ReservationStatus) *observatory.Condition {
	if c, ok := conditions.Find(status.Conditions, observatory.ConditionEditsPending); ok {
		return &c
	}
	return nil
}
