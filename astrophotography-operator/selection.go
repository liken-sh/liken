package main

// Which target a reservation images next. A reservation images only the
// Targets in its own namespace. The runner picks the eligible target
// with the highest priority, then the oldest, then the first by name.
//
// A runner claims a target by writing its reservation name to the
// target's status.reservation, using the resourceVersion it read. When
// two telescopes pick the same target at the same time, one write
// succeeds and the other gets a 409. The second runner reads the target
// again, finds it claimed, and moves on to the next target.

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// eligible reports whether the reservation named self can claim a
// target, before the horizon check (horizon.go). A target is eligible
// when it is not suspended or being deleted, its Complete condition is
// not True, no other Ready reservation has claimed it, the session has
// an imaging camera for its opticalTrain, and it has frames left. ready
// is the set of Ready reservations in the namespace.
func eligible(t *astrophotography.Target, session *observatory.Session, ready map[string]bool, self string) bool {
	if t.Spec.Suspend || t.Metadata.DeletionTimestamp != nil {
		return false
	}
	if c, ok := conditions.Find(t.Status.Conditions, astrophotography.ConditionComplete); ok && c.Status == conditions.True {
		return false
	}
	if owner := t.Status.Reservation; owner != "" && owner != self && ready[owner] {
		return false
	}
	if _, ok := cameraFor(session, t); !ok {
		return false
	}
	_, left := nextSequence(aligned(t))
	return left
}

// byPriority sorts targets in the order a runner picks them.
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

// failedOn reports whether status.failed records a failure of the
// reservation on the target's current generation.
func failedOn(t *astrophotography.Target, reservation string) bool {
	f := t.Status.Failed
	return f != nil && f.Reservation == reservation && f.ObservedGeneration == t.Metadata.Generation
}

// claimNext claims the first eligible target in byPriority order and
// returns it, or returns nil when no target is eligible. On the way it
// releases the targets this reservation can no longer image and marks
// the targets below the horizon. It skips a target that failed on this
// reservation until a person edits it, so the runner does not retry a
// target with a missing filter all night. The failure is recorded in
// status.failed, not in memory, so a runner that starts after an
// operator restart skips the same targets.
func (r *runner) claimNext() (*astrophotography.Target, error) {
	session := r.res.Status.Session
	targets, err := r.o.listTargets(r.namespace)
	if err != nil {
		return nil, err
	}
	ready := r.o.readyIn(r.namespace)
	ready[r.name] = true
	slices.SortFunc(targets, byPriority)
	limit, now := r.skyLimit(), time.Now()
	r.nextRise = time.Time{}
	for i := range targets {
		t := &targets[i]
		if failedOn(t, r.name) {
			continue
		}
		if !eligible(t, session, ready, r.name) {
			if err := r.releaseIneligible(t); err != nil {
				return nil, err
			}
			continue
		}
		if b := limit.check(t, now); b != nil {
			r.noteRise(b)
			if err := r.markBelow(t, b); err != nil {
				return nil, err
			}
			continue
		}
		claimed := false
		// The claim leaves the Imaging condition alone. The slew sets it
		// to Slewing after the filter check, so a target that fails the
		// filter check posts no Slewing Event.
		_, err := r.o.writeTarget(t, func(fresh *astrophotography.Target, _ *statusChange) bool {
			claimed = eligible(fresh, session, ready, r.name) && !failedOn(fresh, r.name)
			if !claimed {
				return false
			}
			fresh.Status.Reservation = r.name
			// failedOn skipped a failure on the current generation, so
			// this one is from before a person edited the target.
			if f := fresh.Status.Failed; f != nil && f.Reservation == r.name {
				fresh.Status.Failed = nil
			}
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

// releaseIneligible releases a target that this reservation has
// claimed when the target is being deleted, is suspended, or has no
// frames left, with the matching reason. It does nothing in every other
// case, including when another reservation or none has claimed the
// target.
func (r *runner) releaseIneligible(t *astrophotography.Target) error {
	_, left := nextSequence(aligned(t))
	switch {
	case t.Status.Reservation != r.name:
		return nil
	case t.Metadata.DeletionTimestamp != nil:
		return r.release(t, astrophotography.ReasonDeleting, deletingMessage)
	case t.Spec.Suspend:
		return r.release(t, astrophotography.ReasonSuspended, "The target is suspended and keeps its progress")
	case !left:
		return r.release(t, astrophotography.ReasonSequencesDone,
			fmt.Sprintf("Reservation %s exposed every frame of the sequences, and each frame counts as archived when promotion moves it", r.name))
	}
	return nil
}
