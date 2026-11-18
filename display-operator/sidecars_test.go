package main

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// The index looks pods up by node, because a capture request names a
// node and not a pod.
func TestTheIndexLooksUpByNode(t *testing.T) {
	index := newSidecarIndex()
	index.add(readySidecar("node-1", "10.42.0.7"))

	pod, held := index.on("node-1")
	if !held {
		t.Fatal("the index has no pod for node-1, want the pod it was given")
	}
	if pod.IP != "10.42.0.7" || !pod.Ready {
		t.Errorf("the pod for node-1 is %+v, want IP 10.42.0.7 and ready", pod)
	}
	if _, held := index.on("node-2"); held {
		t.Error("the index has a pod for node-2, want none because it was never given one")
	}
}

func readySidecar(node, ip string) Pod {
	return Pod{
		Metadata: PodMeta{Namespace: sidecarNamespace, Name: "display-operator-" + node},
		Spec:     PodSpec{NodeName: node},
		Status: PodStatus{
			PodIP:      ip,
			Conditions: []PodCondition{{Type: "Ready", Status: conditionTrue}},
		},
	}
}

// The index keeps a pod that is not Ready, so a request can report
// "not ready" instead of "no sidecar".
func TestTheIndexKeepsAPodThatIsNotReady(t *testing.T) {
	index := newSidecarIndex()
	pod := readySidecar("node-1", "10.42.0.7")
	pod.Status.Conditions = []PodCondition{{Type: "Ready", Status: conditionFalse}}
	index.add(pod)

	held, there := index.on("node-1")
	if !there || held.Ready {
		t.Errorf("the pod in the index is %+v, want a pod that is not ready", held)
	}
}

// A pod with no node cannot serve a request, so the index does not
// keep it.
func TestTheIndexSkipsAPodWithNoNode(t *testing.T) {
	index := newSidecarIndex()
	index.add(Pod{Metadata: PodMeta{Name: "display-operator-pending"}})
	if _, held := index.on(""); held {
		t.Error("the index holds a pod with no node, want none")
	}
}

// Dropping a pod removes it from its node's lookup, and dropping an
// older pod does not remove a newer pod on the same node.
func TestTheIndexDropsAPodThatLeft(t *testing.T) {
	index := newSidecarIndex()
	old := readySidecar("node-1", "10.42.0.7")
	index.add(old)
	index.remove(old)
	if _, held := index.on("node-1"); held {
		t.Error("the index still has a pod that was removed, want none")
	}

	replacement := readySidecar("node-1", "10.42.0.9")
	replacement.Metadata.Name = "display-operator-node-1-again"
	index.add(replacement)
	index.remove(old)
	if pod, held := index.on("node-1"); !held || pod.IP != "10.42.0.9" {
		t.Errorf("after an older pod was removed, the pod that replaced it is %+v, want it kept", pod)
	}
}

// The watch selects the sidecar pods by label, its first list fills
// the index, and each later change updates one pod.
func TestTheIndexFollowsTheWatch(t *testing.T) {
	pods := newObjectStore(t, "/api/v1/namespaces/"+sidecarNamespace+"/pods", "v1", "Pod")
	pods.put(readySidecar("node-1", "10.42.0.7"))
	index := newSidecarIndex()
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	t.Cleanup(func() {
		stop()
		<-done
	})
	go func() {
		defer close(done)
		index.run(ctx, pods.watcher(), sidecarNamespace)
	}()
	eventually(t, "the first read", func() bool {
		_, held := index.on("node-1")
		return held
	})

	pods.put(readySidecar("node-2", "10.42.1.3"))
	eventually(t, "the new pod", func() bool {
		pod, held := index.on("node-2")
		return held && pod.IP == "10.42.1.3"
	})
	pods.remove("display-operator-node-1")
	eventually(t, "the deletion", func() bool {
		_, held := index.on("node-1")
		return !held
	})

	for _, query := range pods.asked() {
		if got := query.Get("labelSelector"); got != sidecarSelector {
			t.Errorf("the request selects %q, want %q", got, sidecarSelector)
		}
	}
}

// A deleted pod leaves the index whatever form the deletion takes. A
// tombstone with a copy converts to the pod. A tombstone with no copy,
// and an object that does not convert, are removed by key.
func TestADeletionDropsThePod(t *testing.T) {
	mistyped := podObject(t, readySidecar("node-1", "10.42.0.7"))
	mistyped.Object["spec"] = "not a spec"
	cases := []struct {
		name    string
		deleted any
	}{
		{name: "a tombstone with a copy", deleted: cache.DeletedFinalStateUnknown{
			Key: sidecarNamespace + "/display-operator-node-1",
			Obj: podObject(t, readySidecar("node-1", "10.42.0.7")),
		}},
		{name: "a tombstone with no copy", deleted: cache.DeletedFinalStateUnknown{
			Key: sidecarNamespace + "/display-operator-node-1",
		}},
		{name: "that does not convert", deleted: mistyped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newSidecarIndex()
			index.add(readySidecar("node-1", "10.42.0.7"))

			index.handler("the test sidecars").OnDelete(c.deleted)

			if _, held := index.on("node-1"); held {
				t.Error("the index still has a deleted pod, want none")
			}
		})
	}
}

// podObject returns pod in the form the informer passes to a handler.
func podObject(t *testing.T, pod Pod) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
	if err != nil {
		t.Fatalf("converting the pod to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// sidecarNamed returns a sidecar pod on node-1 with the given name,
// readiness, and deletion state.
func sidecarNamed(name string, ready, deleting bool) Pod {
	pod := readySidecar("node-1", "10.42.0.7")
	pod.Metadata.Name = name
	pod.Status.PodIP = map[string]string{"old": "10.42.0.7", "new": "10.42.0.8"}[name]
	if !ready {
		pod.Status.Conditions = []PodCondition{{Type: "Ready", Status: conditionFalse}}
	}
	if deleting {
		stamp := "2026-09-27T12:00:00Z"
		pod.Metadata.DeletionTimestamp = &stamp
	}
	return pod
}

// A rollout replaces a node's sidecar, and the events of the two pods
// can arrive interleaved. on gives the reasons for its order.
func TestTheIndexReturnsTheNodesCurrentSidecar(t *testing.T) {
	cases := []struct {
		name   string
		events func(index *sidecarIndex)
		want   string
		ready  bool
	}{
		{name: "the old pod is marked for deletion after the new one is ready", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("old", true, false))
			index.add(sidecarNamed("new", true, false))
			index.add(sidecarNamed("old", true, true))
		}, want: "new", ready: true},
		{name: "the old pod is deleted after the new one is ready", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("old", true, false))
			index.add(sidecarNamed("new", true, false))
			index.add(sidecarNamed("old", false, true))
			index.remove(sidecarNamed("old", false, true))
		}, want: "new", ready: true},
		{name: "a ready pod before one that is starting", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("old", true, false))
			index.add(sidecarNamed("new", false, false))
		}, want: "old", ready: true},
		{name: "a ready pod being deleted before one that is starting", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("new", false, false))
			index.add(sidecarNamed("old", true, true))
		}, want: "old", ready: true},
		{name: "a starting pod before one being deleted that is not ready", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("new", false, false))
			index.add(sidecarNamed("old", false, true))
		}, want: "new", ready: false},
		{name: "a pod being deleted when it is the only one", events: func(index *sidecarIndex) {
			index.add(sidecarNamed("old", false, true))
		}, want: "old", ready: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newSidecarIndex()
			c.events(index)

			held, there := index.on("node-1")
			if !there || held.Name != c.want || held.Ready != c.ready {
				t.Errorf("the index has %+v (found: %v), want %s with ready=%v", held, there, c.want, c.ready)
			}
		})
	}
}
