package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTheIndexAnswersWhichPodIsOnANode(t *testing.T) {
	index := newPodIndex()
	index.replace([]pod{samplePod("node-1"), samplePod("node-2")})

	held, found := index.on("node-1")
	if !found {
		t.Fatal("the index holds no pod for node-1")
	}
	if held.IP != "10.42.0.7" || !held.Ready {
		t.Errorf("the pod is %+v", held)
	}
	if _, found := index.on("node-9"); found {
		t.Error("the index answered for a node it holds nothing for")
	}
}

func TestAPodWhoseCaptureContainerIsNotRunningIsNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.ContainerStatuses[0].Ready = false
	index := newPodIndex()
	index.replace([]pod{held})

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod whose capture container is not running was read as ready")
	}
}

func TestAPodWithNoAddressIsNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.PodIP = ""
	index := newPodIndex()
	index.replace([]pod{held})

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod with no address was read as ready")
	}
}

func TestAPodWithAnotherContainerReadyIsStillNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.ContainerStatuses[0].Name = "operator"
	index := newPodIndex()
	index.replace([]pod{held})

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("the operator container's readiness was read as the capture container's")
	}
}

func TestAPodThatIsNotScheduledIsNotHeld(t *testing.T) {
	held := samplePod("")
	index := newPodIndex()
	index.replace([]pod{held})
	if _, found := index.on(""); found {
		t.Error("a pod with no node was held")
	}
	index.apply("ADDED", held)
	if _, found := index.on(""); found {
		t.Error("an event for a pod with no node was applied")
	}
}

func TestAnEventReplacesOrRemovesOnePod(t *testing.T) {
	index := newPodIndex()
	index.replace([]pod{samplePod("node-1")})

	changed := samplePod("node-1")
	changed.Status.PodIP = "10.42.0.9"
	index.apply("MODIFIED", changed)
	held, _ := index.on("node-1")
	if held.IP != "10.42.0.9" {
		t.Errorf("the pod's address is %q", held.IP)
	}

	index.apply("DELETED", changed)
	if _, found := index.on("node-1"); found {
		t.Error("a deleted pod is still held")
	}
}

func TestAListReplacesEveryPodTheIndexHeld(t *testing.T) {
	index := newPodIndex()
	index.replace([]pod{samplePod("node-1"), samplePod("node-2")})
	// A node whose pod is gone by the time the list arrives leaves the
	// index with it.
	index.replace([]pod{samplePod("node-2")})
	if _, found := index.on("node-1"); found {
		t.Error("a pod the list does not hold is still held")
	}
	if _, found := index.on("node-2"); !found {
		t.Error("a pod the list holds was dropped")
	}
}

func TestTheNamespaceComesFromTheDownwardAPI(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "audio-system")
	if got := podNamespace(); got != "audio-system" {
		t.Errorf("the namespace is %q", got)
	}
	t.Setenv("POD_NAMESPACE", "")
	if got := podNamespace(); got != "liken-system" {
		t.Errorf("the namespace is %q, want liken-system", got)
	}
}

// podEvent is one watch event for the operator's pod on a node, at a
// resource version.
func podEvent(kind, node, version string) string {
	held := samplePod(node)
	body, _ := json.Marshal(map[string]any{
		"type": kind,
		"object": map[string]any{
			"metadata": map[string]string{"name": held.Metadata.Name, "resourceVersion": version},
			"spec":     held.Spec,
			"status":   held.Status,
		},
	})
	return string(body)
}

// A pod watch that the API server ends opens again from the version of
// the last event, and does not list the pods again. The index keeps
// what the events put in it.
func TestThePodWatchResumesFromTheLastEvent(t *testing.T) {
	fake := newOneObject(t)
	index := newPodIndex()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	watch := podWatch(NewClient(fake.server.URL, fake.server.Client(), ""), "liken-system", index, func(error) {})
	watch.retry, watch.retryLimit = time.Millisecond, 10*time.Millisecond
	go watch.run(ctx)

	listed := next(t, fake.listed, "list")
	next(t, fake.watched, "watch")
	fake.events <- podEvent("ADDED", "node-1", "11")
	fake.events <- endWatch
	reopened := next(t, fake.watched, "second watch")

	if !strings.Contains(listed, "labelSelector=app%3Daudio-operator") {
		t.Errorf("the list %q does not select the operator's pods", listed)
	}
	if !strings.Contains(reopened, "resourceVersion=11") {
		t.Errorf("the second watch %q does not resume from the last event", reopened)
	}
	if len(fake.listed) != 0 {
		t.Error("a watch that ended cleanly was followed by a list")
	}
	if held, found := index.on("node-1"); !found || held.IP != "10.42.0.7" {
		t.Errorf("the index holds %+v, %v for node-1", held, found)
	}
}

// A DaemonSet can start a node's new pod before the old pod's final
// DELETED event arrives. That event names the old pod, so it leaves
// the new pod in the index, and only a delete of the held pod removes
// it.
func TestADeleteRemovesOnlyThePodItNames(t *testing.T) {
	old := samplePod("node-1")
	replacement := samplePod("node-1")
	replacement.Metadata.Name = "audio-operator-x7k2p"
	replacement.Status.PodIP = "10.42.0.9"

	cases := []struct {
		name    string
		deleted pod
		held    bool
	}{
		{"the old pod's late delete", old, true},
		{"the held pod's delete", replacement, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newPodIndex()
			index.apply("ADDED", replacement)
			index.apply("DELETED", c.deleted)
			if _, found := index.on("node-1"); found != c.held {
				t.Errorf("the index holds a pod for node-1: %v, want %v", found, c.held)
			}
		})
	}
}

// operatorPod is the node-1 pod named name, created at the minute
// given, and in the state that leaving names: "" for a running pod,
// "deleting" for one with a deletion timestamp, and "Failed" or
// "Succeeded" for a terminal phase.
func operatorPod(name string, minute int, leaving string) pod {
	held := samplePod("node-1")
	held.Metadata.Name = name
	held.Metadata.CreationTimestamp = time.Date(2026, 9, 27, 12, minute, 0, 0, time.UTC)
	switch leaving {
	case "":
	case "deleting":
		deleted := held.Metadata.CreationTimestamp.Add(time.Hour)
		held.Metadata.DeletionTimestamp = &deleted
	default:
		held.Status.Phase = leaving
	}
	return held
}

// Two pods can be on one node while a DaemonSet replaces one. An event
// for the other pod replaces the held one only when it is the newer
// pod, and a pod that is leaving never replaces a running one. An
// event for the held pod itself always replaces it.
func TestAnEventForAnotherPodOnTheNodeReplacesOnlyWithTheNewerPod(t *testing.T) {
	cases := []struct {
		name  string
		held  pod
		event pod
		want  string
	}{
		{"the old pod's late update while it is deleted",
			operatorPod("new", 5, ""), operatorPod("old", 1, "deleting"), "new"},
		{"the old pod's late update once it failed",
			operatorPod("new", 5, ""), operatorPod("old", 1, "Failed"), "new"},
		{"a newer pod while the held one is deleted",
			operatorPod("old", 1, "deleting"), operatorPod("new", 5, ""), "new"},
		{"an older running pod",
			operatorPod("new", 5, ""), operatorPod("old", 1, ""), "new"},
		{"a newer running pod",
			operatorPod("old", 1, ""), operatorPod("new", 5, ""), "new"},
		{"the held pod itself going away",
			operatorPod("new", 5, ""), operatorPod("new", 5, "deleting"), "new"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newPodIndex()
			index.apply("ADDED", c.held)
			index.apply("MODIFIED", c.event)
			if held, _ := index.on("node-1"); held.Name != c.want {
				t.Errorf("the index holds %q, want %q", held.Name, c.want)
			}
		})
	}
}

// A list taken while a DaemonSet replaces a pod holds both, in any
// order, and the index keeps the running one.
func TestAListWithTwoPodsOnANodeHoldsTheRunningOne(t *testing.T) {
	index := newPodIndex()
	index.replace([]pod{operatorPod("new", 5, ""), operatorPod("old", 1, "deleting")})
	if held, _ := index.on("node-1"); held.Name != "new" {
		t.Errorf("the index holds %q, want new", held.Name)
	}
}

// A pod that is leaving answers no tap, even when it is the only pod
// the index holds for its node.
func TestAPodThatIsLeavingIsNotReady(t *testing.T) {
	index := newPodIndex()
	index.apply("MODIFIED", operatorPod("old", 1, "deleting"))
	if held, _ := index.on("node-1"); held.Ready {
		t.Error("a pod being deleted was read as ready")
	}
}
