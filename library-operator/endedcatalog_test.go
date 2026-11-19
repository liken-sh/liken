package main

// What these tests read: the Catalog's Ready condition while the kubelet
// ends the catalog pod again and again. Each retry is a new pod, and the
// condition keeps its reason until a pod is ready, so a retry loop posts
// one condition Event and not one for each retry.

import (
	"testing"
	"testing/synctest"
	"time"
)

// rejectTheCatalogPod runs a Catalog pass every backstop interval for a
// span of the bubble's clock, and after each pass ends each new catalog
// pod the way a kubelet rejects it at admission.
func rejectTheCatalogPod(t *testing.T, cluster *fakeCluster, operator *operator, span time.Duration) {
	start := time.Now()
	for time.Since(start) < span {
		operator.reconcileCatalogs(t.Context(), oneNamespace("house", cluster.heldCatalog("house")),
			nil, nil, time.Now())
		cluster.mutex.Lock()
		if pod := cluster.pods["house-catalog-0"]; pod != nil && pod.Status.Phase == "" {
			pod.Status = PodStatus{Phase: podFailed, Reason: "OutOfcpu", Message: "Node didn't have enough resource: cpu"}
		}
		cluster.mutex.Unlock()
		time.Sleep(backstopInterval)
	}
}

// conditionEvents is the Events of a list that a condition transition
// posted, which leaves out the Events of the replacements.
func conditionEvents(events []posted) []posted {
	var out []posted
	for _, event := range events {
		if event.Reason != reasonPodRecreated {
			out = append(out, event)
		}
	}
	return out
}

func TestACatalogPodThatTheKubeletEndsAgainAndAgainPostsOneConditionEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pod := ended(testCatalogPod(housekeepingCatalog(), 0), podFailed, time.Now(), 0)
		cluster := endedCluster(pod)
		seedCatalog(cluster, "house", "house")

		rejectTheCatalogPod(t, cluster, testOperator(t, cluster), 3*time.Minute)

		want := []posted{{"Warning", catalogReasonPodFailed,
			"the kubelet ended the catalog pod: The node was low on resource: memory"}}
		assertPosted(t, conditionEvents(postedAbout(cluster, "Catalog", "house")), want)
	})
}

// replacement is the catalog pod that replaced an ended one, with a
// count of tries, in a phase and with its containers ready or not.
func replacement(phase string, ready bool, reason string) *Pod {
	pod := readyCatalogPod("house", "house")
	pod.Metadata.Annotations[triesAnnotation] = "2"
	pod.Status.Phase = phase
	pod.Status.Reason = reason
	pod.Status.InitContainerStatuses[0].Ready = ready
	pod.Status.ContainerStatuses[0].Ready = ready
	return pod
}

// A replacement holds PodFailed only until it runs. Once it runs, the
// ordinary rules decide, whatever its count, because a pod that ran for
// days and then went unready is not a replacement that has not started.
func TestAReplacementHoldsPodFailedOnlyUntilItRuns(t *testing.T) {
	cases := []struct {
		name    string
		pod     *Pod
		reason  string
		message string
	}{
		{name: "a replacement that is ready", pod: replacement(podRunning, true, "")},
		{name: "a replacement that runs and is not ready", pod: replacement(podRunning, false, ""),
			reason: catalogReasonPodPending, message: "the catalog pod runs and not every container is ready"},
		{name: "a replacement that has not started", pod: replacement(podPending, false, ""),
			reason: catalogReasonPodFailed, message: "the kubelet ended the catalog pod, and its replacement is not ready yet"},
		{name: "a replacement the kubelet gave a reason", pod: replacement(podPending, false, "ContainerCreating"),
			reason:  catalogReasonPodFailed,
			message: "the kubelet ended the catalog pod, and its replacement is not ready yet: ContainerCreating"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			reason, message := catalogPodBlocker(one.pod)

			if reason != one.reason || message != one.message {
				t.Errorf("blocker = %q %q, want %q %q", reason, message, one.reason, one.message)
			}
		})
	}
}
