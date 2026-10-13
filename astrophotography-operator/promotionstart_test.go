package main

// The promotion loop against the fake API server: when it starts a
// Job, and what the Job runs.

import (
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A frame that the runner counts in an outbox starts a Job, which runs
// the operator's image in its promote mode with both claims mounted.
func TestAPendingFrameStartsAPromotionJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		api.put(trainsPath, object(observatoryNS, "east-imaging", nil, map[string]any{"telescope": "east"}))
		api.put(camerasPath, object(observatoryNS, "east-main", nil, map[string]any{"opticalTrain": "east-imaging"}))
		putTarget(t, api, withPending(m42(), 1, 0))
		startOperator(t, api)
		synctest.Wait()

		job := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-1")
		c := job.Spec.Template.Spec.Containers
		wantArgs := []string{"promote", "--target", "m42", "--path", "astro", "--cameras",
			`{"east-main":{"telescope":"east","train":"east-imaging"}}`}
		if len(c) != 1 || c[0].Image != operatorImage || !reflect.DeepEqual(c[0].Args, wantArgs) {
			t.Fatalf("the Job runs %+v", c)
		}
		wantMounts := []volumeMount{{"outbox", "/outbox"}, {"archive", "/archive"}}
		wantVolumes := []volume{{"outbox", &claimName{"frames"}}, {"archive", &claimName{"archive"}}}
		if !reflect.DeepEqual(c[0].VolumeMounts, wantMounts) || !reflect.DeepEqual(job.Spec.Template.Spec.Volumes, wantVolumes) {
			t.Errorf("the Job mounts %+v from %+v", c[0].VolumeMounts, job.Spec.Template.Spec.Volumes)
		}
		security := job.Spec.Template.Spec.SecurityContext
		if security.RunAsUser != 1000 || security.FSGroup != 1000 || security.FSGroupChangePolicy != "OnRootMismatch" ||
			!c[0].SecurityContext.ReadOnlyRootFilesystem {
			t.Errorf("the Job runs as %+v and %+v", security, c[0].SecurityContext)
		}
		if job.Spec.BackoffLimit != 2 || job.Spec.ActiveDeadlineSeconds != 3600 || job.Spec.TTLSecondsAfterFinished != 3600 {
			t.Errorf("the Job's limits are %+v", job.Spec)
		}
		if job.Metadata.Annotations[annotationPending] != "1" || !job.Metadata.ownedBy("uid-m42") {
			t.Errorf("the Job's metadata is %+v", job.Metadata)
		}
		if job.Spec.Template.Metadata.Labels[labelManagedBy] != managedBy {
			t.Errorf("the pod's labels are %v, so the pod watch does not hold it", job.Spec.Template.Metadata.Labels)
		}
	})
}

// When the archive is the outbox's own claim, the Job mounts it once
// and moves each frame by a rename.
func TestTheSameClaimIsMountedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		target := m42()
		target.Spec.Archive.ClaimName = "frames"
		putTarget(t, api, withPending(target, 1, 0))
		startOperator(t, api)
		synctest.Wait()

		spec := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-1").Spec.Template.Spec
		if len(spec.Volumes) != 1 || !slices.Contains(spec.Containers[0].Args, "--same-claim") {
			t.Errorf("the Job mounts %+v and runs %v", spec.Volumes, spec.Containers[0].Args)
		}
	})
}

// reservation is a Reservation held by this operator that writes to the
// outbox claim frames.
func reservation(phase observatory.ReservationPhase) map[string]any {
	r := object(observatoryNS, "east-tonight", nil, map[string]any{
		"telescope": "east", "holder": managedBy, "outbox": map[string]any{"claimName": "frames"},
	})
	r["status"] = map[string]any{"phase": string(phase)}
	return r
}

// The cases where the loop starts no Job yet.
func TestNoJobStarts(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*testing.T, *fakeAPI)
	}{
		{"with no frame pending", func(t *testing.T, api *fakeAPI) {
			putTarget(t, api, m42())
		}},
		{"after the reservation, while the reservation runs", func(t *testing.T, api *fakeAPI) {
			target := withPending(m42(), 1, 0)
			target.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
			putTarget(t, api, target)
			api.put(reservationsPath, reservation(observatory.ReservationReady))
		}},
		{"while another target's Job uses the outbox claim", func(t *testing.T, api *fakeAPI) {
			putTarget(t, api, withPending(m42(), 1, 0))
			other := object(observatoryNS, "m43-promote-1", map[string]any{labelManagedBy: managedBy, labelComponent: componentPromotion}, nil)
			other["metadata"].(map[string]any)["annotations"] = map[string]any{annotationClaim: "frames", annotationNumber: "1"}
			api.put(jobsPath, other)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				putClaim(api, "frames")
				putClaim(api, "archive")
				c.prepare(t, api)
				startOperator(t, api)
				synctest.Wait()

				if job := api.get(jobsPath, observatoryNS, "m42-promote-1"); job != nil {
					t.Error("the loop started a Job")
				}
			})
		})
	}
}

// With AfterReservation, the Job starts when the reservation that
// writes to the outbox is Released.
func TestAfterReservationPromotesWhenTheReservationIsReleased(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		target := withPending(m42(), 1, 0)
		target.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
		putTarget(t, api, target)
		api.put(reservationsPath, reservation(observatory.ReservationReady))
		startOperator(t, api)
		synctest.Wait()

		api.put(reservationsPath, reservation(observatory.ReservationReleased))
		synctest.Wait()

		if job := api.get(jobsPath, observatoryNS, "m42-promote-1"); job == nil {
			t.Error("no Job started after the reservation was Released")
		}
	})
}

// A target whose archive claim does not exist starts no Job, and says
// why.
func TestAMissingArchiveClaimStartsNoJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putTarget(t, api, withPending(m42(), 1, 0))
		startOperator(t, api)
		synctest.Wait()

		got, _ := conditions.Find(readTarget(api).Status.Conditions, astrophotography.ConditionArchiveAvailable)
		if got.Status != conditions.False || got.Reason != astrophotography.ReasonClaimMissing ||
			got.Message != "the PersistentVolumeClaim archive does not exist in observatory" {
			t.Errorf("ArchiveAvailable is %+v", got)
		}
		if posted := api.recorded.About("Target", "m42"); len(posted) != 1 || posted[0].Type != "Warning" {
			t.Errorf("the Events are %+v", posted)
		}
		if job := api.get(jobsPath, observatoryNS, "m42-promote-1"); job != nil {
			t.Error("the loop started a Job")
		}
	})
}

// A Job whose pod no node can start reports the scheduler's message.
func TestAPodThatCannotStartIsUnschedulable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 1, 0))
		startOperator(t, api)
		synctest.Wait()

		pod := object(observatoryNS, "m42-promote-1-x7k2p", map[string]any{labelManagedBy: managedBy, labelPromotionJob: "m42-promote-1"}, nil)
		pod["status"] = map[string]any{"conditions": []any{map[string]any{"type": "PodScheduled", "status": "False",
			"reason": "Unschedulable", "message": "0/1 nodes are available: 1 node(s) had volume node affinity conflict."}}}
		api.put(podsPath, pod)
		synctest.Wait()

		got, _ := conditions.Find(readTarget(api).Status.Conditions, astrophotography.ConditionArchiveAvailable)
		want := "the pod of the promotion Job m42-promote-1 cannot start: 0/1 nodes are available: 1 node(s) had volume node affinity conflict."
		if got.Reason != astrophotography.ReasonUnschedulable || got.Message != want {
			t.Errorf("ArchiveAvailable is %+v", got)
		}
	})
}

// The Jobs run the operator's own image, read from its own pod. A pod
// that cannot be read, or that has no operator container, starts no
// Job, and the loop tries again after its backoff.
func TestTheJobWaitsForTheOperatorsImage(t *testing.T) {
	cases := []struct {
		name string
		pod  map[string]any
	}{
		{"a pod that cannot be read", nil},
		{"a pod with no operator container", operatorPod("sidecar")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := startFakeAPI(t)
				putClaim(api, "frames")
				putClaim(api, "archive")
				putTarget(t, api, withPending(m42(), 1, 0))
				if c.pod != nil {
					api.put(podsPath, c.pod)
				}
				runOperator(t, api)
				synctest.Wait()
				if api.get(jobsPath, observatoryNS, "m42-promote-1") != nil {
					t.Fatal("a Job started with no image")
				}

				api.put(podsPath, operatorPod("operator"))
				time.Sleep(lastRetry)
				synctest.Wait()

				job := decode[jobObject](api, jobsPath, observatoryNS, "m42-promote-1")
				if containers := job.Spec.Template.Spec.Containers; len(containers) != 1 || containers[0].Image != operatorImage {
					t.Errorf("the Job runs %+v", containers)
				}
			})
		})
	}
}

// A reservation's session records its outbox, and a spec edit during
// the session does not move it, so the pacing reads the session's.
func TestAfterReservationReadsTheSessionsOutbox(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		target := withPending(m42(), 1, 0)
		target.Spec.Archive.Promotion = astrophotography.PromotionAfterReservation
		putTarget(t, api, target)
		edited := reservation(observatory.ReservationReady)
		edited["spec"].(map[string]any)["outbox"] = map[string]any{"claimName": "other"}
		edited["status"].(map[string]any)["session"] = map[string]any{"outbox": map[string]any{"claimName": "frames"}}
		api.put(reservationsPath, edited)
		startOperator(t, api)
		synctest.Wait()

		if api.get(jobsPath, observatoryNS, "m42-promote-1") != nil {
			t.Error("the loop started a Job while the session writes to the outbox")
		}
	})
}
