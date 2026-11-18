package main

// A session runs from its record (records.go), not from the specs, so
// a spec edit during a session takes effect at the next session. A
// device that an edit moves reports this in its ParentFound condition
// (serveruse.go). The Reservation's EditsPending condition reports it
// for spec.outbox and for the FITS keywords of each imaging camera,
// merged from the Observatory, the Telescope, the Camera, and the
// Reservation. The condition is True while an edit waits, and its
// message lists each value that will change, now and at the next
// session.
//
// The condition can change only when a spec changes or a session is
// recorded, so the runner evaluates it in observe, recordSession, and
// recordAgain, not on every status write. A reservation has the
// condition from the PowerOn step's record until it is Released.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	reasonNoEditPending = "NoEditPending"
	reasonEditPending   = "EditPending"
)

// updateEditsPending sets the runner's EditsPending condition from the
// session record and the specs in t, and returns whether it changed.
func (r *runner) updateEditsPending(t *tree) bool {
	next := r.sessionCondition(t)
	if sameState(next, r.session) {
		return false
	}
	r.session = next
	return true
}

// sameState reports whether two conditions, either of which can be
// nil, have the same status, reason, and message. It ignores the
// transition time, because a condition built again has the current
// time, and the status write keeps the stored time (mergeConditions).
func sameState(a, b *observatory.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}

// sessionCondition returns the EditsPending condition, or nil when the
// reservation has no session. The PowerOn step records the outbox and
// the keywords in the same write as the telescope's devices, so a
// record with no devices has nothing to compare yet.
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

// outboxPending describes a change of the outbox's claim name.
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

// keywordsPending describes each change of the merged FITS keywords of
// the session's imaging cameras. It merges over the devices that the
// session recorded, because a device or a Guider that an edit adds or
// moves is reported elsewhere (serveruse.go, guidersOf). It skips a
// camera that was deleted during the session.
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

// keywordChanges describes each keyword of one camera whose value or
// comment changes: first the keywords of next, in order, then the
// keywords that next drops. It also reports a change of order alone,
// because the driver writes the keywords into the header in the order
// it receives them.
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

// storedSession returns the EditsPending condition in a status, or nil.
// A new runner starts from the stored condition, so an operator restart
// posts no Event.
func storedSession(status observatory.ReservationStatus) *observatory.Condition {
	if c, ok := conditions.Find(status.Conditions, observatory.ConditionEditsPending); ok {
		return &c
	}
	return nil
}
