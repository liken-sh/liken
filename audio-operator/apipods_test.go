package main

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"
)

func TestTheIndexReturnsThePodOnANode(t *testing.T) {
	index := newPodIndex()
	index.put(samplePod("node-1"))
	index.put(samplePod("node-2"))

	held, found := index.on("node-1")
	if !found {
		t.Fatal("the index has no pod for node-1, want the pod at 10.42.0.7")
	}
	if held.IP != "10.42.0.7" || !held.Ready {
		t.Errorf("the pod is %+v, want a Ready pod at 10.42.0.7", held)
	}
	if _, found := index.on("node-9"); found {
		t.Error("the index returned a pod for node-9, want none")
	}
}

func TestAPodWhoseCaptureContainerIsNotRunningIsNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.ContainerStatuses[0].Ready = false
	index := newPodIndex()
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod whose capture container is not running is Ready, want not Ready")
	}
}

func TestAPodWithNoAddressIsNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.PodIP = ""
	index := newPodIndex()
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod with no address is Ready, want not Ready")
	}
}

func TestAPodWithAnotherContainerReadyIsStillNotReady(t *testing.T) {
	held := samplePod("node-1")
	held.Status.ContainerStatuses[0].Name = "operator"
	index := newPodIndex()
	index.put(held)

	found, _ := index.on("node-1")
	if found.Ready {
		t.Error("a pod whose only Ready container is operator is Ready, want not Ready")
	}
}

func TestAPodThatIsNotScheduledIsNotIndexed(t *testing.T) {
	held := samplePod("")
	index := newPodIndex()
	index.put(held)
	if _, found := index.on(""); found {
		t.Error("the index returned a pod with no node, want none")
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
		t.Errorf("the pod's address is %q, want 10.42.0.9", held.IP)
	}

	index.forget(changed.Metadata.Name)
	if _, found := index.on("node-1"); found {
		t.Error("the index returned a deleted pod, want none")
	}
}

func TestTheNamespaceComesFromTheDownwardAPI(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "audio-system")
	if got := podNamespace(); got != "audio-system" {
		t.Errorf("the namespace is %q, want audio-system", got)
	}
	t.Setenv("POD_NAMESPACE", "")
	if got := podNamespace(); got != "liken-system" {
		t.Errorf("the namespace is %q, want liken-system", got)
	}
}

// podEvent returns the JSON of a watch event of the given type for a
// pod, at the resourceVersion version.
func podEvent(t *testing.T, kind string, held pod, version string) string {
	t.Helper()
	object := podObject(t, held)
	object.SetResourceVersion(version)
	return encode(t, map[string]any{"type": kind, "object": object.Object})
}

// podObject converts a pod to an object with the apiVersion, kind, and
// namespace that the API server includes.
func podObject(t *testing.T, held pod) *unstructured.Unstructured {
	t.Helper()
	object := asObject(t, held)
	object.SetAPIVersion("v1")
	object.SetKind("Pod")
	object.SetNamespace("liken-system")
	return object
}

func podRead(t *testing.T, pods ...pod) string {
	t.Helper()
	items := []any{}
	for _, held := range pods {
		items = append(items, podObject(t, held).Object)
	}
	return encode(t, items)
}

// The watch runs through client-go's reflector against a fake API
// server, and every request must carry the operator's label selector.
func TestThePodWatchFollowsThePodsIntoTheIndex(t *testing.T) {
	moved := samplePod("node-1")
	moved.Status.PodIP = "10.42.0.9"
	synctest.Test(t, func(t *testing.T) {
		pods := newWatchServer("/api/v1/namespaces/liken-system/pods", "v1", "Pod",
			[]string{podRead(t, samplePod("node-1"))},
			[]string{pause, podEvent(t, "MODIFIED", moved, "2"), podEvent(t, "ADDED", samplePod("node-2"), "3"),
				pause, podEvent(t, "DELETED", moved, "4"), holdOpen})
		index := newPodIndex()
		watchPods(t.Context(), testWatcher(t, serveCollections(t, nil, pods)), "liken-system", index)
		synctest.Wait()

		wantPodOn(t, index, "node-1", "10.42.0.7")
		pods.release()
		synctest.Wait()
		wantPodOn(t, index, "node-2", "10.42.0.7")
		wantPodOn(t, index, "node-1", "10.42.0.9")
		pods.release()
		synctest.Wait()
		wantPodOn(t, index, "node-1", "")

		if len(pods.requests()) == 0 {
			t.Error("the watch sent no request")
		}
		for _, query := range pods.requests() {
			if !strings.Contains(query, "labelSelector=app%3Daudio-operator") {
				t.Errorf("the request %q does not select the operator's pods", query)
			}
		}
	})
}

// wantPodOn expects no pod for node when address is empty.
func wantPodOn(t *testing.T, index *podIndex, node, address string) {
	t.Helper()
	held, found := index.on(node)
	if (address == "" && found) || (address != "" && (!found || held.IP != address)) {
		t.Errorf("the index returns %+v, %v for %s, want the address %q", held, found, node, address)
	}
}

// When the watch misses the delete of a pod, the informer passes a
// cache.DeletedFinalStateUnknown, whose Obj can be nil.
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
				t.Error("the index returned the removed pod, want none")
			}
		})
	}
}

func TestAPodThatDoesNotConvertLeavesTheIndexAsItWas(t *testing.T) {
	index := newPodIndex()
	index.put(samplePod("node-1"))
	mistyped := asObject(t, samplePod("node-1"))
	mistyped.Object["status"] = map[string]any{"podIP": 7}
	podHandler{index}.handler().OnUpdate(asObject(t, samplePod("node-1")), mistyped)
	if held, _ := index.on("node-1"); held.IP != "10.42.0.7" {
		t.Errorf("the index returns %+v, want the pod at 10.42.0.7", held)
	}
}

// A DaemonSet can start a node's new pod before the DELETED event of the
// old pod arrives.
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
		{"the indexed pod's delete", replacement, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newPodIndex()
			index.put(replacement)
			index.forget(c.deleted.Metadata.Name)
			if _, found := index.on("node-1"); found != c.held {
				t.Errorf("the index has a pod for node-1: %v, want %v", found, c.held)
			}
		})
	}
}

// operatorPod returns a pod on node-1 created minute minutes past 12:00
// on a fixed day. leaving is "" for a running pod, "deleting" for a pod
// with a deletionTimestamp, or a phase such as "Failed".
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

// A node has two pods while a DaemonSet replaces one. A leaving pod
// never replaces a running one.
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
		{"a newer pod while the indexed one is deleted",
			operatorPod("old", 1, "deleting"), operatorPod("new", 5, ""), "new"},
		{"an older running pod",
			operatorPod("new", 5, ""), operatorPod("old", 1, ""), "new"},
		{"a newer running pod",
			operatorPod("old", 1, ""), operatorPod("new", 5, ""), "new"},
		{"the indexed pod itself going away",
			operatorPod("new", 5, ""), operatorPod("new", 5, "deleting"), "new"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newPodIndex()
			index.put(c.held)
			index.put(c.event)
			if held, _ := index.on("node-1"); held.Name != c.want {
				t.Errorf("the index returns %q, want %q", held.Name, c.want)
			}
		})
	}
}

// A list taken while a DaemonSet replaces a pod contains both pods in
// any order, and the index returns the running one.
func TestAListWithTwoPodsOnANodeKeepsTheRunningOne(t *testing.T) {
	running, leaving := operatorPod("new", 5, ""), operatorPod("old", 1, "deleting")
	for _, order := range [][]pod{{running, leaving}, {leaving, running}} {
		index := newPodIndex()
		for _, held := range order {
			index.put(held)
		}
		if held, _ := index.on("node-1"); held.Name != "new" {
			t.Errorf("after %s then %s, the index returns %q, want new",
				order[0].Metadata.Name, order[1].Metadata.Name, held.Name)
		}
	}
}

// After a 410 Gone, the reflector lists the pods again.
func TestAPodGoneFromANewReadLeavesTheIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expired := `{"type":"ERROR","object":{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Expired","code":410,"message":"too old resource version"}}`
		pods := newWatchServer("/api/v1/namespaces/liken-system/pods", "v1", "Pod",
			[]string{podRead(t, samplePod("node-1"), samplePod("node-2")), podRead(t, samplePod("node-2"))},
			[]string{pause, expired})
		index := newPodIndex()
		watchPods(t.Context(), testWatcher(t, serveCollections(t, nil, pods)), "liken-system", index)
		synctest.Wait()
		wantPodOn(t, index, "node-1", "10.42.0.7")

		pods.release()
		time.Sleep(time.Minute)
		synctest.Wait()
		wantPodOn(t, index, "node-1", "")
		wantPodOn(t, index, "node-2", "10.42.0.7")
	})
}

func TestAPodThatIsLeavingIsNotReady(t *testing.T) {
	index := newPodIndex()
	index.put(operatorPod("old", 1, "deleting"))
	if held, _ := index.on("node-1"); held.Ready {
		t.Error("a pod being deleted is Ready, want not Ready")
	}
}
