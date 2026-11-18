package main

// A person confirms that a resource is set up by writing the SetUp
// condition through the status subresource, the way an outside
// controller writes a pod's readiness gate. The status writer replaces
// the whole status on each write, so it copies SetUp from the stored
// status.
//
// SetUp expires on its own, so nobody has to withdraw it in the
// morning: at status.setUpUntil when the person wrote one, and
// otherwise at the first astronomical dawn after its
// lastTransitionTime at the resource's Observatory (dawn.go). The
// operator then sets it to False with the reason Expired. No watch
// event arrives at that moment, so the status writer sets a timer for
// the next expiry.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// withStoredSetUp appends the stored SetUp to the composed conditions, unless
// they already have one.
func withStoredSetUp(stored, composed []observatory.Condition) []observatory.Condition {
	if c, ok := conditions.Find(stored, observatory.ConditionSetUp); ok {
		if _, has := conditions.Find(composed, observatory.ConditionSetUp); !has {
			composed = append(composed, c)
		}
	}
	return composed
}

// setUpExpiry returns when a SetUp that is True expires: at until when
// it is set, else at the first astronomical dawn after its
// lastTransitionTime at place. It returns the zero time when SetUp is
// not True, or when place is nil because the resource has no
// Observatory, such as a device that is not installed in a telescope.
// When such a device is installed later, its expiry is still the first
// dawn after lastTransitionTime.
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

// expireSetUp sets a SetUp that is past its expiry to False with the
// reason Expired. It returns the new condition, or nil when nothing
// changed, so the caller posts the Event only after its status write
// succeeds.
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
	expired, _ := conditions.Find(*list, observatory.ConditionSetUp)
	return &expired
}

// expiryTime returns the value of status.setUpExpiryTime, or nil when
// SetUp does not expire. A person who lets SetUp last until dawn leaves
// setUpUntil unset, and this field is then the only place that `kubectl
// get` shows when SetUp ends.
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

// nextExpiry returns the earlier of earliest and the stored SetUp's
// expiry, when that expiry is in the future, for the status writer's
// timer. A zero earliest means no expiry yet.
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

func placeOf(t *tree, kind observatory.Kind, name string) *observatory.Location {
	if site, ok := t.observatories[t.observatoryOf(kind, name)]; ok {
		return &site.Spec.Location
	}
	return nil
}
