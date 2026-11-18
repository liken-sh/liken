package main

import (
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/astrophotography-operator/promote"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// A pending frame in an outbox starts a Job that runs the operator's
// image in promote mode, with the outbox and archive claims mounted.
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
		wantMounts := []volumeMount{{Name: "outbox", MountPath: "/outbox"}, {Name: "archive", MountPath: "/archive"}}
		wantVolumes := []volume{{Name: "outbox", PersistentVolumeClaim: &claimName{ClaimName: "frames"}}, {Name: "archive", PersistentVolumeClaim: &claimName{ClaimName: "archive"}}}
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
			t.Errorf("the pod's labels are %v, so the pod watch does not select it", job.Spec.Template.Metadata.Labels)
		}
	})
}

// When the archive is in the outbox's claim, the Job mounts the claim
// once and moves each frame with a rename.
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

// m43 returns m42 with another name and UID.
func m43() astrophotography.Target {
	t := m42()
	t.Metadata.Name, t.Metadata.UID = "m43", "uid-m43"
	return t
}

// Two targets with frames in one outbox claim start one Job, because
// only one Job at a time copies from an outbox claim. The second
// target's Job starts when the first Job ends.
func TestTwoTargetsOnOneOutboxClaimStartOneJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putClaim(api, "archive")
		putTarget(t, api, withPending(m42(), 1, 0))
		putTarget(t, api, withPending(m43(), 1, 0))
		startOperator(t, api)
		synctest.Wait()

		want := []string{"POST /apis/batch/v1/namespaces/observatory/jobs"}
		if got := api.writes(jobsPath); !slices.Equal(got, want) {
			t.Errorf("Job writes %q, want %q", got, want)
		}
		if api.get(jobsPath, observatoryNS, "m43-promote-1") != nil {
			t.Error("m43's Job started while m42's Job uses the outbox claim")
		}

		finish(t, api, "m42-promote-1", &promote.Report{Totals: luminance(1), Promoted: 1})
		synctest.Wait()

		if api.get(jobsPath, observatoryNS, "m43-promote-1") == nil {
			t.Error("m43's Job did not start after m42's Job ended")
		}
	})
}

// reservation returns a Reservation that this operator manages, which
// writes frames to the outbox claim.
func reservation(phase observatory.ReservationPhase) map[string]any {
	r := object(observatoryNS, "east-tonight", nil, map[string]any{
		"telescope": "east", "holder": managedBy, "outbox": map[string]any{"claimName": "frames"},
	})
	r["status"] = map[string]any{"phase": string(phase)}
	return r
}

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

// A target whose archive claim does not exist starts no Job, and the
// target's status gives the reason.
func TestAMissingArchiveClaimStartsNoJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		putClaim(api, "frames")
		putTarget(t, api, withPending(m42(), 1, 0))
		startOperator(t, api)
		synctest.Wait()

		got, _ := conditions.Find(readTarget(api).Status.Conditions, astrophotography.ConditionArchiveAvailable)
		if got.Status != conditions.False || got.Reason != astrophotography.ReasonClaimMissing ||
			got.Message != "The PersistentVolumeClaim archive that spec.archive.claimName refers to does not exist in namespace observatory" {
			t.Errorf("ArchiveAvailable is %+v", got)
		}
		if got, want := targetEvents(api), []string{"Normal WorkLeft", "Warning ClaimMissing"}; !slices.Equal(got, want) {
			t.Errorf("the Events are %q, want %q", got, want)
		}
		if job := api.get(jobsPath, observatoryNS, "m42-promote-1"); job != nil {
			t.Error("the loop started a Job")
		}
	})
}

// When no node can run a Job's pod, the target's status reports the
// scheduler's message.
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
		want := "The pod of the promotion Job m42-promote-1 cannot start: 0/1 nodes are available: 1 node(s) had volume node affinity conflict."
		if got.Reason != astrophotography.ReasonUnschedulable || got.Message != want {
			t.Errorf("ArchiveAvailable is %+v", got)
		}
	})
}

// Jobs run the operator's own image, which the operator reads from its
// own pod. When the pod cannot be read or has no operator container,
// no Job starts, and the loop tries again after its backoff.
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
// the session does not change it, so AfterReservation reads the
// outbox from the session.
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
