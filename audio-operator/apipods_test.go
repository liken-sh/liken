package main

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

func TestTheIndexAnswersWhichPodIsOnANode(t *testing.T) {
	index := newPodIndex()
	index.put(samplePod("node-1"))
	index.put(samplePod("node-2"))

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
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod whose capture container is not running was read as ready")
	}
}

func TestAPodWithNoAddressIsNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.PodIP = ""
	index := newPodIndex()
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod with no address was read as ready")
	}
}

func TestAPodWithAnotherContainerReadyIsStillNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.ContainerStatuses[0].Name = "operator"
	index := newPodIndex()
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("the operator container's readiness was read as the capture container's")
	}
}

func TestAPodThatIsNotScheduledIsNotHeld(t *testing.T) {
	held := samplePod("")
	index := newPodIndex()
	index.put(held)
	if _, found := index.on(""); found {
		t.Error("a pod with no node was held")
	}
}

func TestAnEventReplacesOrRemovesOnePod(t *testing.T) {
	index := newPodIndex()
	index.put(samplePod("node-1"))

	changed := samplePod("node-1")
	changed.Status.PodIP = "10.42.0.9"
	index.put(changed)
	held, _ := index.on("node-1")
	if held.IP != "10.42.0.9" {
		t.Errorf("the pod's address is %q", held.IP)
	}

	index.forget(changed.Metadata.Name)
	if _, found := index.on("node-1"); found {
		t.Error("a deleted pod is still held")
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

// podEvent is one watch event for a pod.
func podEvent(t *testing.T, kind string, held pod, version string) string {
	t.Helper()
	object := podObject(t, held)
	object.SetResourceVersion(version)
	return encode(t, map[string]any{"type": kind, "object": object.Object})
}

// podObject is a pod as the API server sends it, with its kind.
func podObject(t *testing.T, held pod) *unstructured.Unstructured {
	t.Helper()
	object := asObject(t, held)
	object.SetAPIVersion("v1")
	object.SetKind("Pod")
	object.SetNamespace("liken-system")
	return object
}

// podRead is one read of the pods, as a JSON array.
func podRead(t *testing.T, pods ...pod) string {
	t.Helper()
	items := []any{}
	for _, held := range pods {
		items = append(items, podObject(t, held).Object)
	}
	return encode(t, items)
}

// Through the reflector: the watch selects the operator's pods by
// label, and a pod the watch adds, changes, and deletes moves the
// index with it.
func TestThePodWatchFollowsThePodsIntoTheIndex(t *testing.T) {
	moved := samplePod("node-1")
	moved.Status.PodIP = "10.42.0.9"
	pods := newWatchServer("/api/v1/namespaces/liken-system/pods", "v1", "Pod",
		[]string{podRead(t, samplePod("node-1"))},
		[]string{pause, podEvent(t, "MODIFIED", moved, "2"), podEvent(t, "ADDED", samplePod("node-2"), "3"),
			pause, podEvent(t, "DELETED", moved, "4"), holdOpen})
	index := newPodIndex()
	watchPods(watchContext(t), testWatcher(t, serveCollections(t, nil, pods)), "liken-system", index)
	pods.awaitWatches(t, 1)

	holds(t, index, "node-1", "10.42.0.7")
	pods.release()
	holds(t, index, "node-2", "10.42.0.7")
	holds(t, index, "node-1", "10.42.0.9")
	pods.release()
	holds(t, index, "node-1", "")

	for _, query := range pods.requests() {
		if !strings.Contains(query, "labelSelector=app%3Daudio-operator") {
			t.Errorf("the request %q does not select the operator's pods", query)
		}
	}
}

// holds waits until the index holds the pod at address for node, or
// holds no pod for node when address is empty.
func holds(t *testing.T, index *podIndex, node, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		held, found := index.on(node)
		if (address == "" && !found) || (found && held.IP == address) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	held, found := index.on(node)
	t.Fatalf("the index holds %+v, %v for %s, want the address %q", held, found, node, address)
}

// A pod deleted while the watch was down arrives as a tombstone, which
// can hold no copy of the pod. Its key still names the pod.
func TestAPodRemovedWhileTheWatchWasDownIsForgotten(t *testing.T) {
	held := samplePod("node-1")
	for _, c := range []struct {
		name    string
		removed any
	}{
		{"a tombstone with a copy", cache.DeletedFinalStateUnknown{Key: "liken-system/" + held.Metadata.Name, Obj: asObject(t, held)}},
		{"a tombstone with no copy", cache.DeletedFinalStateUnknown{Key: "liken-system/" + held.Metadata.Name}},
		{"a removed pod", asObject(t, held)},
	} {
		t.Run(c.name, func(t *testing.T) {
			index := newPodIndex()
			index.put(held)
			podHandler{index}.handler().OnDelete(c.removed)
			if _, found := index.on("node-1"); found {
				t.Error("the removed pod is still held")
			}
		})
	}
}

// A pod that does not convert leaves the index as it was.
func TestAPodThatDoesNotConvertLeavesTheIndexAsItWas(t *testing.T) {
	index := newPodIndex()
	index.put(samplePod("node-1"))
	mistyped := asObject(t, samplePod("node-1"))
	mistyped.Object["status"] = map[string]any{"podIP": 7}
	podHandler{index}.handler().OnUpdate(asObject(t, samplePod("node-1")), mistyped)
	if held, _ := index.on("node-1"); held.IP != "10.42.0.7" {
		t.Errorf("the index holds %+v", held)
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
			index.put(replacement)
			index.forget(c.deleted.Metadata.Name)
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
			index.put(c.held)
			index.put(c.event)
			if held, _ := index.on("node-1"); held.Name != c.want {
				t.Errorf("the index holds %q, want %q", held.Name, c.want)
			}
		})
	}
}

// A list taken while a DaemonSet replaces a pod holds both, in any
// order, and the index keeps the running one.
func TestAListWithTwoPodsOnANodeHoldsTheRunningOne(t *testing.T) {
	running, leaving := operatorPod("new", 5, ""), operatorPod("old", 1, "deleting")
	for _, order := range [][]pod{{running, leaving}, {leaving, running}} {
		index := newPodIndex()
		for _, held := range order {
			index.put(held)
		}
		if held, _ := index.on("node-1"); held.Name != "new" {
			t.Errorf("after %s then %s, the index holds %q, want new",
				order[0].Metadata.Name, order[1].Metadata.Name, held.Name)
		}
	}
}

// Through the reflector: a pod that is gone when the watch reads the
// pods again, after a 410, leaves the index.
func TestAPodGoneFromANewReadLeavesTheIndex(t *testing.T) {
	expired := `{"type":"ERROR","object":{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410,"message":"too old resource version"}}`
	pods := newWatchServer("/api/v1/namespaces/liken-system/pods", "v1", "Pod",
		[]string{podRead(t, samplePod("node-1"), samplePod("node-2")), podRead(t, samplePod("node-2"))},
		[]string{pause, expired})
	index := newPodIndex()
	watchPods(watchContext(t), testWatcher(t, serveCollections(t, nil, pods)), "liken-system", index)
	pods.awaitWatches(t, 1)
	holds(t, index, "node-1", "10.42.0.7")

	pods.release()
	holds(t, index, "node-1", "")
	holds(t, index, "node-2", "10.42.0.7")
}

// A pod that is leaving answers no tap, even when it is the only pod
// the index holds for its node.
func TestAPodThatIsLeavingIsNotReady(t *testing.T) {
	index := newPodIndex()
	index.put(operatorPod("old", 1, "deleting"))
	if held, _ := index.on("node-1"); held.Ready {
		t.Error("a pod being deleted was read as ready")
	}
}
