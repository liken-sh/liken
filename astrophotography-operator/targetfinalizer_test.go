package main

// The finalizer on a Target: a delete waits while frames of the target
// are pending in an outbox claim, so promotion still moves them.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// The operator puts the finalizer on every target it reads, and keeps
// another controller's finalizer.
func TestATargetCarriesTheFramesFinalizer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		target := m42()
		target.Metadata.Finalizers = []string{"example.com/keep"}
		putTarget(t, api, target)
		startOperator(t, api)
		synctest.Wait()

		if got := readTarget(api).Metadata.Finalizers; !slices.Equal(got, []string{"example.com/keep", framesFinalizer}) {
			t.Errorf("finalizers %v", got)
		}
	})
}

// A person deletes a target while frames of it wait in the outbox. The
// target stays, with Imaging False and Deleting, until promotion moves
// the frames. Then the finalizer comes off and the target goes.
func TestADeletedTargetWaitsForItsPendingFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		target := withPending(m42(), 2, 0)
		putTarget(t, api, target)
		startOperator(t, api)
		synctest.Wait()

		api.delete(targetsPath, observatoryNS, "m42")
		synctest.Wait()

		held := readTarget(api)
		imaging, _ := conditions.Find(held.Status.Conditions, astrophotography.ConditionImaging)
		if held.Metadata.DeletionTimestamp == nil || imaging.Status != conditions.False || imaging.Reason != astrophotography.ReasonDeleting {
			t.Fatalf("the target is %+v, Imaging %+v", held.Metadata, imaging)
		}
		if got := targetEvents(api); !slices.Contains(got, "Normal "+astrophotography.ReasonDeleting) {
			t.Errorf("Events %v, want Normal Deleting", got)
		}

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(2), Promoted: 2})
		synctest.Wait()

		if api.get(targetsPath, observatoryNS, "m42") != nil {
			t.Error("the target stays after its frames reached the archive")
		}
	})
}

// A delete goes at once when no frame waits, and when the outbox claim
// that held the frames is gone with them.
func TestADeletedTargetGoesWhenNoFrameCanWait(t *testing.T) {
	cases := []struct {
		name   string
		target astrophotography.Target
	}{
		{"no frame pending", m42()},
		{"the outbox claim is gone", withPending(m42(), 2, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				putClaim(api, "archive")
				putTarget(t, api, c.target)
				startOperator(t, api)
				synctest.Wait()

				api.delete(targetsPath, observatoryNS, "m42")
				synctest.Wait()

				if api.get(targetsPath, observatoryNS, "m42") != nil {
					t.Error("the target stays")
				}
			})
		})
	}
}
