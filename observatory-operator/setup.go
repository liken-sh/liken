package main

// A person's SetUp. A person writes the SetUp condition into a
// resource's status through the status subresource, as an outside
// controller writes a pod's readiness gate, and the status writer,
// which composes and replaces each status, must keep it as the person
// wrote it. Plan 21 gives the design.
//
// A confirmation expires by itself, so nobody has to remember to
// withdraw it in the morning: at status.setUpUntil when the person
// wrote one, and otherwise at the first astronomical dawn after its
// lastTransitionTime at the resource's Observatory (dawn.go). The
// operator then sets it False with the reason Expired, and the
// transition posts one Event. The expiry is a clock, so the status
// writer keeps a timer for the next one.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// keepSetUp answers a composed list of conditions with the stored
// SetUp added as the person wrote it. The status writer composes each
// status from what it observes, and SetUp is the one condition that a
// person observes.
func keepSetUp(stored, composed []observatory.Condition) []observatory.Condition {
	if c, ok := conditions.Find(stored, observatory.ConditionSetUp); ok {
		if _, has := conditions.Find(composed, observatory.ConditionSetUp); !has {
			composed = append(composed, c)
		}
	}
	return composed
}

// setUpExpiry answers when a SetUp that holds expires: at until when a
// person wrote one, else at the first astronomical dawn after its
// transition at a place, else never, which is the zero time. A
// resource with no Observatory, such as a device on the shelf, has no
// place, and its confirmation expires once it gets one, at the dawn
// after the confirmation there.
func setUpExpiry(c observatory.Condition, until *time.Time, place *observatory.Location) time.Time {
	switch {
	case c.Status != observatory.ConditionTrue:
		return time.Time{}
	case until != nil:
		return *until
	case place != nil:
		return dawnAfter(c.LastTransitionTime, *place)
	}
	return time.Time{}
}

// expireSetUp sets a SetUp that holds past its expiry False, with the
// reason Expired, and answers the transition, which posts its Event
// once the write lands.
func expireSetUp(list *[]observatory.Condition, until *time.Time, place *observatory.Location) *observatory.Condition {
	c, ok := conditions.Find(*list, observatory.ConditionSetUp)
	if !ok {
		return nil
	}
	expiry := setUpExpiry(c, until, place)
	if expiry.IsZero() || time.Now().Before(expiry) {
		return nil
	}
	next := condition(observatory.ConditionSetUp, observatory.ConditionFalse, observatory.ReasonExpired,
		fmt.Sprintf("Expired at %s: %s", expiry.UTC().Format(time.RFC3339), lowerFirst(firstNonEmpty(c.Message, "set up"))))
	next.ObservedGeneration = c.ObservedGeneration
	if !conditions.Set(list, next) {
		return nil
	}
	held, _ := conditions.Find(*list, observatory.ConditionSetUp)
	return &held
}

// expiryTime answers the expiry of the SetUp in a composed list of
// conditions, for status.setUpExpiryTime, or nil when no SetUp holds
// or it never expires. The person's setUpUntil is absent when they let
// the SetUp last until dawn, so without this field `kubectl get` could
// not show when that SetUp ends.
func expiryTime(list []observatory.Condition, until *time.Time, place *observatory.Location) *time.Time {
	c, ok := conditions.Find(list, observatory.ConditionSetUp)
	if !ok {
		return nil
	}
	expiry := setUpExpiry(c, until, place)
	if expiry.IsZero() {
		return nil
	}
	expiry = expiry.UTC()
	return &expiry
}

// nextExpiry answers the earlier of a time and the expiry of a stored
// SetUp, when that expiry is still to come. The status writer waits
// for the earliest one, because no watch event marks the moment.
func nextExpiry(earliest time.Time, stored []observatory.Condition, until *time.Time, place *observatory.Location) time.Time {
	c, ok := conditions.Find(stored, observatory.ConditionSetUp)
	if !ok {
		return earliest
	}
	expiry := setUpExpiry(c, until, place)
	if expiry.IsZero() || !expiry.After(time.Now()) {
		return earliest
	}
	if earliest.IsZero() || expiry.Before(earliest) {
		return expiry
	}
	return earliest
}

// placeOf answers the location of a resource's Observatory, or nil for
// a resource that has none.
func placeOf(t *tree, kind observatory.Kind, name string) *observatory.Location {
	if site, ok := t.observatories[t.observatoryOf(kind, name)]; ok {
		return &site.Spec.Location
	}
	return nil
}
