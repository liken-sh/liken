package main

// What these tests read: what the pass does with a long-running pod
// whose phase is Failed or Succeeded. Each such pod has restartPolicy
// Always, so it reaches either phase only when the kubelet ends it, and
// no kubelet starts it again. The pass waits for the pod's turn, deletes
// it, creates it again with its count of tries raised by one, and posts
// an Event on the object that owns it.

import (
	"net/http"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

// The node every ended pod of these tests ran on. It is Ready, so an
// ended store copy on it is replaced and not left to the heal.
const endedNode = "node-1"

// ended is a pod that the kubelet ended, as a pass reads it: stamped
// with the template the pass builds, created at a time, with a count of
// tries, on endedNode, and with the words the kubelet writes for an
// eviction.
func ended(pod *Pod, phase string, created time.Time, tries int) *Pod {
	if err := stampTemplateHash(&pod.Metadata, pod.Spec); err != nil {
		panic(err)
	}
	if tries > 0 {
		pod.Metadata.Annotations[triesAnnotation] = strconv.Itoa(tries)
	}
	pod.Metadata.CreationTimestamp = created
	pod.Spec.NodeName = endedNode
	pod.Status = PodStatus{
		Phase:   phase,
		Reason:  "Evicted",
		Message: "The node was low on resource: memory.",
	}
	return pod
}

// endedCluster is a cluster that holds one ended pod on a Ready node.
func endedCluster(pod *Pod) *fakeCluster {
	cluster := newFakeCluster()
	cluster.nodes[endedNode] = nodeAt(endedNode, nodeReadyVerdict, time.Hour)
	cluster.pods[pod.Metadata.Name] = pod
	return cluster
}

// endedPod is one kind of pod the operator stands: the pod as this
// operator builds it, the object that owns it, the reason of the Event
// posted on that object, and the step of the pass that stands it.
type endedPod struct {
	name      string
	ownerKind string
	ownerName string
	reason    string
	build     func(o *operator) *Pod
	stand     func(t *testing.T, o *operator, now time.Time)
}

func endedPods() []endedPod {
	return []endedPod{
		{
			name: "the catalog pod", ownerKind: "Catalog", ownerName: "house", reason: reasonPodRecreated,
			build: func(o *operator) *Pod {
				return buildCatalogPod(housekeepingCatalog(), 0, o.scannerImage, o.corrosionImage,
					o.busAddress, o.topicBase)
			},
			stand: func(t *testing.T, o *operator, now time.Time) {
				if _, err := o.standCatalogPod(t.Context(), housekeepingCatalog(), 0, now); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "the progress pod", ownerKind: "Catalog", ownerName: "house", reason: reasonPodRecreated,
			build: func(o *operator) *Pod {
				return buildProgressPod(housekeepingCatalog(), 0, o.scannerImage, o.corrosionImage,
					o.busAddress, o.topicBase, o.mediaTopicBase)
			},
			stand: func(t *testing.T, o *operator, now time.Time) {
				if _, err := o.standProgressPod(t.Context(), housekeepingCatalog(), 0, now); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "the Jellyfin pod", ownerKind: "Catalog", ownerName: "house", reason: reasonPodRecreated,
			build: func(o *operator) *Pod {
				return buildJellyfinPod(jellyfinCatalog(), o.scannerImage, o.busAddress,
					o.topicBase, o.mediaTopicBase)
			},
			stand: func(t *testing.T, o *operator, now time.Time) {
				if _, err := o.standJellyfinPod(t.Context(), jellyfinCatalog(), now); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "the screen pod", ownerKind: "Player", ownerName: "den", reason: reasonScreenPodRecreated,
			build: func(o *operator) *Pod {
				return buildScreenPod(denScreen(), nil, nil, o.browserImage, o.corrosionImage,
					o.topicBase, o.household)
			},
			stand: func(t *testing.T, o *operator, now time.Time) {
				o.reconcileScreens(t.Context(), testLibraryNamespace, nil, []Player{*denScreen()},
					nil, nil, nil, now)
			},
		},
	}
}

// A pod that ran longer than its wait is replaced by the pass that reads
// it: one delete, then one create of the same template, with a count of
// one try. With restartPolicy Always, Succeeded is the kubelet's end as
// much as Failed.
func TestAPodTheKubeletEndedIsCreatedAgain(t *testing.T) {
	for _, one := range endedPods() {
		for _, phase := range []string{podFailed, podSucceeded} {
			t.Run(one.name+" "+phase, func(t *testing.T) {
				operator := testOperator(t, newFakeCluster())
				pod := ended(one.build(operator), phase, testNow.Add(-time.Hour), 0)
				cluster := endedCluster(pod)
				operator = testOperator(t, cluster)

				one.stand(t, operator, testNow)

				replacement := cluster.heldPod(pod.Metadata.Name)
				if replacement == nil {
					t.Fatalf("no pod %s stands after the pass, want it created again", pod.Metadata.Name)
				}
				if replacement.Status.Phase != "" {
					t.Errorf("the pod %s is %s after the pass, want the new pod", pod.Metadata.Name, replacement.Status.Phase)
				}
				if got := replacement.Metadata.Annotations[triesAnnotation]; got != "1" {
					t.Errorf("tries = %q, want 1", got)
				}
				if got := cluster.countRequests(http.MethodDelete, "pods"); got != 1 {
					t.Errorf("deletes = %d, want 1", got)
				}
				if got := cluster.countRequests(http.MethodPost, "pods"); got != 1 {
					t.Errorf("creates = %d, want 1", got)
				}
			})
		}
	}
}

// A pod that ended young waits: 10 seconds for its first try, doubling
// with each try, to a limit of 5 minutes, counted from its creation.
// During the wait the ended pod stays, so a person can read its status.
func TestAnEndedPodWaitsItsTurn(t *testing.T) {
	cases := []struct {
		name     string
		tries    int
		age      time.Duration
		replaced bool
	}{
		{name: "a first pod 5 seconds old", tries: 0, age: 5 * time.Second},
		{name: "a first pod 10 seconds old", tries: 0, age: 10 * time.Second, replaced: true},
		{name: "a third try 30 seconds old", tries: 2, age: 30 * time.Second},
		{name: "a third try 40 seconds old", tries: 2, age: 40 * time.Second, replaced: true},
		{name: "a tenth try 4 minutes old", tries: 9, age: 4 * time.Minute},
		{name: "a tenth try 5 minutes old", tries: 9, age: 5 * time.Minute, replaced: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			pod := ended(testCatalogPod(housekeepingCatalog(), 0), podFailed, testNow.Add(-one.age), one.tries)
			cluster := endedCluster(pod)

			if _, err := testOperator(t, cluster).standCatalogPod(t.Context(), housekeepingCatalog(), 0, testNow); err != nil {
				t.Fatal(err)
			}

			if replaced := cluster.countRequests(http.MethodDelete, "pods") == 1; replaced != one.replaced {
				t.Errorf("the pass replaced the pod %s old with %d tries: %v, want %v",
					one.age, one.tries, replaced, one.replaced)
			}
		})
	}
}

// rejectEachPod runs a pass every backstop interval for a span of the
// bubble's clock, and after each pass ends each new pod the way a
// kubelet rejects it at admission. It answers the time of each create,
// counted from the start.
func rejectEachPod(t *testing.T, one endedPod, cluster *fakeCluster, operator *operator, name string, span time.Duration) []time.Duration {
	start := time.Now()
	var creates []time.Duration
	for time.Since(start) < span {
		one.stand(t, operator, time.Now())
		cluster.mutex.Lock()
		if pod := cluster.pods[name]; pod != nil && pod.Status.Phase == "" {
			creates = append(creates, pod.Metadata.CreationTimestamp.Sub(start))
			pod.Status = PodStatus{Phase: podFailed, Reason: "OutOfcpu", Message: "Node didn't have enough resource: cpu"}
		}
		cluster.mutex.Unlock()
		time.Sleep(backstopInterval)
	}
	return creates
}

// The kubelet rejects each new pod at once, as it does a pod pinned to a
// node that cannot admit it. The creates then follow the doubling wait
// and do not repeat at the rate of the passes.
func TestTheKubeletRejectingEachPodDoublesTheWait(t *testing.T) {
	want := []time.Duration{10 * time.Second, 30 * time.Second, 70 * time.Second, 150 * time.Second,
		310 * time.Second, 610 * time.Second, 910 * time.Second}
	for _, one := range endedPods() {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				operator := testOperator(t, newFakeCluster())
				pod := ended(one.build(operator), podFailed, time.Now(), 0)
				cluster := endedCluster(pod)
				operator = testOperator(t, cluster)

				got := rejectEachPod(t, one, cluster, operator, pod.Metadata.Name, 16*time.Minute)

				if len(got) != len(want) {
					t.Fatalf("creates at %v, want %v", got, want)
				}
				for index := range want {
					if got[index] != want[index] {
						t.Errorf("create %d at %v, want %v", index, got[index], want[index])
					}
				}
			})
		})
	}
}

// The replacement posts one Warning on the object that owns the pod,
// with the kubelet's words, because the pod and its status are gone once
// the pass deletes it.
func TestAPodTheKubeletEndedPostsAnEventOnItsOwner(t *testing.T) {
	for _, one := range endedPods() {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				operator := testOperator(t, newFakeCluster())
				pod := ended(one.build(operator), podFailed, testNow.Add(-time.Hour), 0)
				cluster := endedCluster(pod)
				operator = testOperator(t, cluster)

				one.stand(t, operator, testNow)

				want := []posted{{"Warning", one.reason, "Created the pod " + pod.Metadata.Name +
					" again, because the kubelet ended it: Evicted: The node was low on resource: memory"}}
				assertPosted(t, postedAbout(cluster, one.ownerKind, one.ownerName), want)
			})
		})
	}
}

// endedAfter records how long an ended pod ran before the kubelet ended
// it, the way the kubelet records it: the time of the DisruptionTarget
// condition for an eviction, or the time a container stopped.
func endedAfter(pod *Pod, ran time.Duration, by string) *Pod {
	at := pod.Metadata.CreationTimestamp.Add(ran)
	switch by {
	case "a disruption":
		pod.Status.Conditions = []PodCondition{{Type: "DisruptionTarget", Status: "True", LastTransitionTime: at}}
	case "a container":
		pod.Status.ContainerStatuses = []ContainerStatus{
			{Name: reporterContainer, State: ContainerState{Terminated: &ContainerStateTerminated{FinishedAt: at}}},
		}
	}
	return pod
}

// A retry loop posts its first failure and not each retry. A pod that
// ran longer than the longest wait before the kubelet ended it ended for
// a new cause, so it posts again. A pod that the kubelet rejected at
// admission never ran, however old it is.
func TestOnlyAFirstEndOrALongRunPostsAnEvent(t *testing.T) {
	cases := []struct {
		name   string
		tries  int
		age    time.Duration
		ran    time.Duration
		by     string
		events int
	}{
		{name: "a first pod", tries: 0, age: time.Minute, events: 1},
		{name: "a fourth try that ended young", tries: 3, age: 2 * time.Minute, ran: time.Second, by: "a container"},
		{name: "a seventh try rejected at admission 5 minutes and 1 second ago", tries: 6, age: 5*time.Minute + time.Second},
		{name: "a fourth try evicted after an hour", tries: 3, age: 2 * time.Hour, ran: time.Hour, by: "a disruption", events: 1},
		{name: "a fourth try whose container stopped after an hour", tries: 3, age: 2 * time.Hour, ran: time.Hour, by: "a container", events: 1},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pod := endedAfter(ended(testCatalogPod(housekeepingCatalog(), 0), podFailed,
					testNow.Add(-one.age), one.tries), one.ran, one.by)
				cluster := endedCluster(pod)

				if _, err := testOperator(t, cluster).standCatalogPod(t.Context(), housekeepingCatalog(), 0, testNow); err != nil {
					t.Fatal(err)
				}

				if got := len(postedAbout(cluster, "Catalog", "house")); got != one.events {
					t.Errorf("Events = %d, want %d", got, one.events)
				}
			})
		})
	}
}

// A person who changes the Catalog or the Player after the kubelet ended
// its pod can have fixed what the kubelet refused. So an ended pod of
// another template is replaced at once, and the new pod has no count.
func TestAChangedTemplateReplacesAnEndedPodAtOnce(t *testing.T) {
	pod := ended(testCatalogPod(housekeepingCatalog(), 0), podFailed, testNow.Add(-time.Second), 4)
	pod.Metadata.Annotations[templateHashAnnotation] = "an-older-template"
	cluster := endedCluster(pod)
	operator := testOperator(t, cluster)

	if _, err := operator.standCatalogPod(t.Context(), housekeepingCatalog(), 0, testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.standCatalogPod(t.Context(), housekeepingCatalog(), 0, testNow); err != nil {
		t.Fatal(err)
	}

	replacement := cluster.heldPod(pod.Metadata.Name)
	if replacement == nil || replacement.Status.Phase != "" {
		t.Fatalf("pod = %+v after two passes, want the new pod", replacement)
	}
	if got, held := replacement.Metadata.Annotations[triesAnnotation]; held {
		t.Errorf("tries = %q, want no count on a pod of a changed template", got)
	}
}

// The Event gives what the kubelet wrote, whichever of the reason and
// the message it wrote, with no period at the end of the sentence.
func TestTheEventOfAnEndedPodGivesTheKubeletsWords(t *testing.T) {
	cases := []struct {
		name   string
		status PodStatus
		want   string
	}{
		{name: "a reason and a message",
			status: PodStatus{Reason: "Terminated", Message: "Pod was terminated in response to imminent node shutdown."},
			want:   "Terminated: Pod was terminated in response to imminent node shutdown"},
		{name: "an eviction whose message ends in a period and a space",
			status: PodStatus{Reason: "Evicted", Message: "The node was low on resource: memory. Threshold quantity: 100Mi, available: 50Mi. "},
			want:   "Evicted: The node was low on resource: memory. Threshold quantity: 100Mi, available: 50Mi"},
		{name: "a reason alone", status: PodStatus{Reason: "OutOfmemory"}, want: "OutOfmemory"},
		{name: "a message alone", status: PodStatus{Message: "Preempted by a pod of higher priority"},
			want: "Preempted by a pod of higher priority"},
		{name: "neither", want: "the pod status gives no reason"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			pod := &Pod{Status: one.status}
			pod.Status.Phase = podFailed

			if got := podEndedBy(pod); got != one.want {
				t.Errorf("podEndedBy = %q, want %q", got, one.want)
			}
		})
	}
}
