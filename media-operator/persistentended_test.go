package main

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

// persistentKind is one kind of long-running pod that reconcilePersistent
// keeps: seed adds the claim and the pod as an earlier pass created them,
// and returns the reconcile that the pass runs for their owner.
type persistentKind struct {
	name      string
	pod       string
	claim     string
	ownerKind string
	owner     string
	seed      func(t *testing.T, cluster *fakeCluster, media *operator) func() error
}

var persistentKinds = []persistentKind{
	{
		name: "the idle pod", pod: "theater-idle", claim: "theater-idle-devices",
		ownerKind: "Player", owner: "theater",
		seed: func(t *testing.T, cluster *fakeCluster, media *operator) func() error {
			media.idleDisplayClass = "display-draw"
			player := playerWithDisplay()
			claim := buildIdleClaim(player, media.idleDisplayClass)
			seedPersistent(t, cluster, claim, media.resources.apply(
				plainIdlePod(player, claim, media.busAddress, media.topicBase, "America/New_York")))
			return func() error { return media.reconcileIdle(player, "America/New_York", nil) }
		},
	},
	{
		name: "the reader pod", pod: "sofa-remote", claim: "sofa-remote-devices",
		ownerKind: "Remote", owner: "sofa",
		seed: func(t *testing.T, cluster *fakeCluster, media *operator) func() error {
			remote := remoteWithController()
			claim := buildRemoteClaim(remote)
			seedPersistent(t, cluster, claim, media.resources.apply(
				buildRemotePod(remote, claim, media.sidecarImage, media.busAddress, media.topicBase)))
			return func() error { return media.reconcileRemote(remote, claimRead{}) }
		},
	},
}

// A running pod with the current template is kept, so the replacement
// in the next test follows from the phase alone.
func TestReconcilePersistentKeepsARunningPod(t *testing.T) {
	for _, kind := range persistentKinds {
		t.Run(kind.name, func(t *testing.T) {
			cluster := newFakeCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			reconcile := kind.seed(t, cluster, media)
			cluster.pods[kind.pod].Status = PodStatus{Phase: podRunning}

			mustSucceed(t, reconcile())

			mustMatch(t, countRequests(cluster, http.MethodDelete, "pods"), 0)
		})
	}
}

// endPod marks a pod as the kubelet leaves it when it ends the pod: in
// phase, with the kubelet's reason and message, created at created, and
// with a count of earlier tries when tries is not empty.
func endPod(pod *Pod, phase, reason string, created time.Time, tries string) {
	pod.Status = PodStatus{Phase: phase, Reason: reason, Message: "The node was low on resource: memory."}
	pod.Metadata.CreationTimestamp = created.UTC().Format(time.RFC3339)
	if tries != "" {
		pod.Metadata.Annotations[triesAnnotation] = tries
	}
}

// The kubelet ends a pod with restartPolicy Always only for a cause
// outside the pod, such as an eviction or a node shutdown, and the pod
// stays Failed or Succeeded. A pod that ran for longer than the wait is
// replaced in one pass, the claim is kept, and the new pod counts one
// try.
func TestReconcilePersistentReplacesAPodThatTheKubeletEnded(t *testing.T) {
	phases := []struct {
		phase  string
		reason string
		cause  string
	}{
		{podFailed, "Evicted", "with phase Failed and reason Evicted"},
		{podSucceeded, "Terminated", "with phase Succeeded and reason Terminated"},
	}
	for _, kind := range persistentKinds {
		for _, ended := range phases {
			t.Run(kind.name+" "+ended.phase, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					cluster := newFakeCluster()
					media := testOperator(t, cluster, make(chan struct{}, 1))
					reconcile := kind.seed(t, cluster, media)
					endPod(cluster.pods[kind.pod], ended.phase, ended.reason, time.Now().Add(-time.Hour), "")
					uid := cluster.pods[kind.pod].Metadata.UID

					mustSucceed(t, reconcile())

					replacement, exists := cluster.pods[kind.pod]
					if !exists || replacement.Metadata.UID == uid {
						t.Fatalf("after the pass, the ended pod %s is not replaced, want a new pod (requests: %v)",
							kind.pod, cluster.requests)
					}
					mustMatch(t, replacement.Status.Phase, "")
					mustMatch(t, replacement.Metadata.Annotations[triesAnnotation], "1")
					mustMatch(t, countRequests(cluster, http.MethodDelete, "resourceclaims"), 0)
					mustMatchAll(t, postedOn(cluster, kind.ownerKind, kind.owner), []string{
						"Warning PodRecreated: Deleted pod " + kind.pod + " and created it again, because the kubelet " +
							"ended the pod " + ended.cause + ": The node was low on resource: memory",
					})
				})
			})
		}
	}
}

// createTimes runs a pass each time the operator's wake fires, as the
// kubelet rejects each pod that the pass creates, and returns how long
// after the start each pod was created. A rejection runs the next pass
// at once, as the pod watch does for a pod that becomes Failed. It stops
// after count creates, and fails when no wake comes within an hour.
func createTimes(t *testing.T, cluster *fakeCluster, reconcile func() error, wake chan struct{}, pod string, count int) []time.Duration {
	t.Helper()
	start := time.Now()
	uid := cluster.pods[pod].Metadata.UID
	var times []time.Duration
	for len(times) < count {
		mustSucceed(t, reconcile())
		if current := cluster.pods[pod]; current != nil && current.Metadata.UID != uid {
			times = append(times, time.Since(start))
			uid = current.Metadata.UID
			endPod(current, podFailed, "OutOfcpu", time.Now(), "")
			continue
		}
		select {
		case <-wake:
		case <-time.After(time.Hour):
			t.Fatalf("after the creates at %v, no pass woke within an hour, want a wake at the next deadline", times)
		}
	}
	return times
}

// A device claim pins each of these pods to one node, so the kubelet can
// reject the replacement at once. Each replacement waits from the ended
// pod's creation, 10 seconds for the first and twice as long for each
// try after it, to a limit of 5 minutes. Only the first replacement
// posts an Event.
func TestReconcilePersistentWaitsLongerForEachPodTheKubeletRejects(t *testing.T) {
	for _, kind := range persistentKinds {
		t.Run(kind.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				wake := make(chan struct{}, 1)
				media := testOperator(t, cluster, wake)
				reconcile := kind.seed(t, cluster, media)
				endPod(cluster.pods[kind.pod], podFailed, "OutOfcpu", time.Now(), "")

				times := createTimes(t, cluster, reconcile, wake, kind.pod, 7)

				mustMatchAll(t, times, []time.Duration{
					10 * time.Second, 30 * time.Second, 70 * time.Second, 150 * time.Second,
					310 * time.Second, 610 * time.Second, 910 * time.Second,
				})
				mustMatch(t, len(postedOn(cluster, kind.ownerKind, kind.owner)), 1)
			})
		})
	}
}

// stamp formats a time as the API server writes it, in whole seconds.
func stamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

// stoppedByDisruption records that the kubelet ended the pod at the
// given time with the DisruptionTarget condition.
func stoppedByDisruption(pod *Pod, at time.Time) {
	pod.Status.Conditions = append(pod.Status.Conditions,
		PodCondition{Type: "DisruptionTarget", Status: "True", LastTransitionTime: stamp(at)})
}

// stoppedContainer records that the pod's container terminated at the
// given time.
func stoppedContainer(pod *Pod, at time.Time) {
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, ContainerStatus{
		Name: "idle", State: ContainerState{Terminated: &ContainerStateTerminated{ExitCode: 143, FinishedAt: stamp(at)}},
	})
}

// A replacement after a later try posts an Event only when the ended pod
// ran for longer than 5 minutes before the kubelet ended it, because a
// retry loop posts its first failure and not each retry. A pod that the
// kubelet rejected at admission never ran, however old it is.
func TestAReplacedPodPostsAnEventForTheFirstTryOrALongRun(t *testing.T) {
	cases := []struct {
		name  string
		tries string
		age   time.Duration
		stop  func(pod *Pod, created time.Time)
		want  int
	}{
		{"the first try", "", time.Minute, func(*Pod, time.Time) {}, 1},
		{"a later try that ended young", "3", 2 * time.Minute,
			func(pod *Pod, created time.Time) { stoppedByDisruption(pod, created.Add(time.Second)) }, 0},
		{"the seventh try, rejected at admission and replaced a second after its wait", "6",
			5*time.Minute + time.Second, func(*Pod, time.Time) {}, 0},
		{"a later try that the kubelet ended after ten minutes", "3", 11 * time.Minute,
			func(pod *Pod, created time.Time) { stoppedByDisruption(pod, created.Add(10*time.Minute)) }, 1},
		{"a later try whose container stopped after ten minutes", "3", 11 * time.Minute,
			func(pod *Pod, created time.Time) { stoppedContainer(pod, created.Add(10*time.Minute)) }, 1},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				kind := persistentKinds[0]
				reconcile := kind.seed(t, cluster, media)
				created := time.Now().Add(-each.age)
				endPod(cluster.pods[kind.pod], podFailed, "OutOfcpu", created, each.tries)
				each.stop(cluster.pods[kind.pod], created)

				mustSucceed(t, reconcile())

				mustMatch(t, len(postedOn(cluster, kind.ownerKind, kind.owner)), each.want)
			})
		})
	}
}

// When a person changed the object after the kubelet ended its pod,
// perhaps to fix what the kubelet refused, the pod is replaced at once
// and the new pod has no count of tries.
func TestAnEndedPodWithAChangedTemplateIsReplacedAtOnce(t *testing.T) {
	for _, kind := range persistentKinds {
		t.Run(kind.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				reconcile := kind.seed(t, cluster, media)
				ended := cluster.pods[kind.pod]
				endPod(ended, podFailed, "OutOfcpu", time.Now(), "3")
				ended.Metadata.Annotations[templateHashAnnotation] = "a-template-before-the-edit"
				uid := ended.Metadata.UID

				mustSucceed(t, reconcile())

				replacement, exists := cluster.pods[kind.pod]
				if !exists || replacement.Metadata.UID == uid {
					t.Fatalf("after the pass, the ended pod %s with a changed template is not replaced, want a new pod at once (requests: %v)",
						kind.pod, cluster.requests)
				}
				mustMatch(t, replacement.Metadata.Annotations[triesAnnotation], "")
			})
		})
	}
}
