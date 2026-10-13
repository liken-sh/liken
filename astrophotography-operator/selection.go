package main

// Which targets a reservation images (plan 01). A Reservation of this
// operator images the targets of its own namespace. A target is
// eligible when it is not suspended, its Complete condition is not
// True, it has work left, its opticalTrain is one of the session's
// trains or empty, and no other Ready reservation images it now. The
// runner takes the eligible target with the highest priority, then the
// oldest, then the first name.
//
// A runner claims a target by writing its own reservation into the
// target's status.reservation, with the resourceVersion it read. Two
// telescopes that choose one target at the same moment get one success
// and one 409, and the second runner reads the target again, finds it
// claimed, and chooses again.

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// eligible reports whether a runner can claim a target now. ready names
// the Ready reservations of the namespace, and self the runner's own.
func eligible(t *astrophotography.Target, session *observatory.Session, ready map[string]bool, self string) bool {
	if t.Spec.Suspend || t.Metadata.DeletionTimestamp != nil {
		return false
	}
	if c, ok := conditions.Find(t.Status.Conditions, astrophotography.ConditionComplete); ok && c.Status == conditions.True {
		return false
	}
	if held := t.Status.Reservation; held != "" && held != self && ready[held] {
		return false
	}
	if _, ok := cameraFor(session, t); !ok {
		return false
	}
	_, left := nextSequence(aligned(t))
	return left
}

// byPriority orders the targets the way a runner takes them: the
// highest priority, then the oldest, then the first name.
func byPriority(a, b astrophotography.Target) int {
	if c := cmp.Compare(b.Spec.Priority, a.Spec.Priority); c != 0 {
		return c
	}
	if c := created(a).Compare(created(b)); c != 0 {
		return c
	}
	return cmp.Compare(a.Metadata.Name, b.Metadata.Name)
}

func created(t astrophotography.Target) time.Time {
	if t.Metadata.CreationTimestamp == nil {
		return time.Time{}
	}
	return *t.Metadata.CreationTimestamp
}

// claimNext claims the first eligible target in the order of
// byPriority, and answers it, or nil when none is eligible. A target
// that fails on this reservation is not taken again until a person
// edits it, so one target with a wrong filter does not hold the night.
func (r *runner) claimNext() (*astrophotography.Target, error) {
	session := r.res.Status.Session
	targets, err := r.o.listTargets(r.namespace)
	if err != nil {
		return nil, err
	}
	ready := r.o.readyIn(r.namespace)
	ready[r.name] = true
	slices.SortFunc(targets, byPriority)
	for i := range targets {
		t := &targets[i]
		if generation, failed := r.skipped[t.Metadata.UID]; failed && generation == t.Metadata.Generation {
			continue
		}
		if !eligible(t, session, ready, r.name) {
			if err := r.releaseIneligible(t); err != nil {
				return nil, err
			}
			continue
		}
		claimed := false
		_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
			claimed = eligible(fresh, session, ready, r.name)
			if !claimed {
				return false
			}
			fresh.Status.Reservation = r.name
			change.setImaging(fresh, true, astrophotography.ReasonSlewing,
				fmt.Sprintf("Reservation %s slews to %s.", r.name, fresh.Spec.Object))
			return true
		})
		if err != nil {
			return nil, fmt.Errorf("claiming target %s: %w", t.Metadata.Name, err)
		}
		if claimed {
			return t, nil
		}
	}
	return nil, nil
}

// releaseIneligible ends this reservation's claim of a target it can no
// longer image, such as a target whose last frame a restart counted, or
// one a person suspended while the operator was down.
// Any other claim of this reservation that it cannot image came from
// another writer, and stays as that writer left it.
func (r *runner) releaseIneligible(t *astrophotography.Target) error {
	_, left := nextSequence(aligned(t))
	switch {
	case t.Status.Reservation != r.name:
		return nil
	case t.Spec.Suspend:
		return r.release(t, astrophotography.ReasonSuspended, "The target is suspended. It keeps its progress.")
	case !left:
		return r.release(t, astrophotography.ReasonSequencesDone,
			fmt.Sprintf("Reservation %s exposed every frame the sequences want. Promotion counts them as archived.", r.name))
	}
	return nil
}
