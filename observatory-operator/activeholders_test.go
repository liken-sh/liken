package main

// The Observatory's Active condition names the reservations that hold
// telescopes in it now, and keeps its transition time while they
// change.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func TestTheObservatorysActiveNamesItsHoldersNow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := bothReady(t)
		active := func() observatory.Condition {
			site, _ := decode[observatory.Observatory](t, w.api, kindCollection(observatory.ObservatoryKind), "lab")
			return conditionOf(site.Status.Conditions, observatory.ConditionActive)
		}
		var before observatory.Condition
		w.until(time.Minute, "Active does not name both holders", func() bool {
			before = active()
			return before.Message == "Active for Reservation east-tonight, west-tonight"
		})
		w.api.deleteNamed(kindCollection(observatory.ReservationKind), "east-tonight")
		var after observatory.Condition
		w.until(10*time.Minute, "Active does not name the holder that stays", func() bool {
			after = active()
			return after.Message == "Active for Reservation west-tonight"
		})
		if after.Status != observatory.ConditionTrue || !after.LastTransitionTime.Equal(before.LastTransitionTime) {
			t.Errorf("Active = %s since %v, want True since %v", after.Status, after.LastTransitionTime, before.LastTransitionTime)
		}
	})
}
