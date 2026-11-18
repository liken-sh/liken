package main

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// The operator adds the finalizer to every Target it reads, and leaves
// another controller's finalizer in place.
func TestATargetHasTheFramesFinalizer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		target := m42()
		target.Metadata.Finalizers = []string{"example.com/keep"}
		putTarget(t, api, target)
		startOperator(t, api)
		synctest.Wait()

		if got := readTarget(api).Metadata.Finalizers; !slices.Equal(got, []string{"example.com/keep", framesFinalizer}) {
			t.Errorf("the finalizers are %v, want example.com/keep and %s", got, framesFinalizer)
		}
	})
}

// A person deletes a Target while frames of it are waiting in the
// outbox. The Target remains, with Imaging False and reason Deleting,
// until promotion moves the frames. Then the operator removes the
// finalizer and the Target is deleted.
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

		stored := readTarget(api)
		imaging, _ := conditions.Find(stored.Status.Conditions, astrophotography.ConditionImaging)
		if stored.Metadata.DeletionTimestamp == nil || imaging.Status != conditions.False || imaging.Reason != astrophotography.ReasonDeleting {
			t.Fatalf("the target metadata is %+v and Imaging is %+v, want a deletion timestamp and Imaging False with reason Deleting", stored.Metadata, imaging)
		}
		if got := targetEvents(api); !slices.Contains(got, "Normal "+astrophotography.ReasonDeleting) {
			t.Errorf("the Events are %v, want Normal Deleting", got)
		}

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(2), Promoted: 2})
		synctest.Wait()

		if api.get(targetsPath, observatoryNS, "m42") != nil {
			t.Error("the Target still exists after promotion moved its frames to the archive, want it deleted")
		}
	})
}

// A delete proceeds at once when no frame is waiting, and when the
// outbox claim that held the frames is gone.
func TestADeletedTargetIsRemovedWhenNoFrameCanWait(t *testing.T) {
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
					t.Error("the Target still exists after its deletion, want it deleted")
				}
			})
		})
	}
}
