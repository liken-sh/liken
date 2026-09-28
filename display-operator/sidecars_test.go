package main

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// A pod is held under the node it runs on, because a capture goes to
// a node and not to a pod.
func TestTheIndexAnswersByNode(t *testing.T) {
	index := newSidecarIndex()
	index.hold(readySidecar("node-1", "10.42.0.7"))

	pod, held := index.on("node-1")
	if !held {
		t.Fatal("the index answers nothing for the node it was given")
	}
	if pod.IP != "10.42.0.7" || !pod.Ready {
		t.Errorf("the index answers %+v", pod)
	}
	if _, held := index.on("node-2"); held {
		t.Error("the index answers for a node it was never given")
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

// A pod the kubelet does not call ready is still held, because the
// answer a request needs is "not ready" and not "no sidecar".
func TestAPodThatIsNotReadyIsHeldAsSuch(t *testing.T) {
	index := newSidecarIndex()
	pod := readySidecar("node-1", "10.42.0.7")
	pod.Status.Conditions = []PodCondition{{Type: "Ready", Status: conditionFalse}}
	index.hold(pod)

	held, there := index.on("node-1")
	if !there || held.Ready {
		t.Errorf("the index answers %+v, want a pod that is not ready", held)
	}
}

// A pod with no node yet answers no request, so it is not held at
// all.
func TestAPodWithNoNodeIsNotHeld(t *testing.T) {
	index := newSidecarIndex()
	index.hold(Pod{Metadata: PodMeta{Name: "display-operator-pending"}})
	if _, held := index.on(""); held {
		t.Error("a pod with no node was held")
	}
}

// A pod that left takes its node's answer with it, and a newer pod
// on the node is not dropped by an older pod's departure.
func TestAPodThatLeftIsDropped(t *testing.T) {
	index := newSidecarIndex()
	old := readySidecar("node-1", "10.42.0.7")
	index.hold(old)
	index.drop(old)
	if _, held := index.on("node-1"); held {
		t.Error("the index still answers for a pod that left")
	}

	replacement := readySidecar("node-1", "10.42.0.9")
	replacement.Metadata.Name = "display-operator-node-1-again"
	index.hold(replacement)
	index.drop(old)
	if pod, held := index.on("node-1"); !held || pod.IP != "10.42.0.9" {
		t.Errorf("an older pod's departure dropped the pod that replaced it: %+v", pod)
	}
}

// The watch asks for the sidecar pods by label, its first read fills
// the index, and each change after it moves one pod.
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
			t.Errorf("a request selected %q, want %q", got, sidecarSelector)
		}
	}
}

// A deleted pod leaves the index even when the index cannot read it: a
// tombstone with a copy, a tombstone with no copy, and a pod whose
// fields do not convert each drop it by its key.
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
			index.hold(readySidecar("node-1", "10.42.0.7"))

			index.handler("the test sidecars").OnDelete(c.deleted)

			if _, held := index.on("node-1"); held {
				t.Error("the index still answers for a deleted pod")
			}
		})
	}
}

// podObject is a pod the way the informer hands it to a handler.
func podObject(t *testing.T, pod Pod) *unstructured.Unstructured {
	t.Helper()
	fields, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: fields}
}

// A sidecar pod by name on node-1, ready or not, and deleting or not.
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
// arrive interleaved. The index answers per node with a Ready pod
// first, even one being deleted, because it still serves captures
// through its grace period, and a DaemonSet rollout with no surge
// starts the new pod only after the old one begins to leave. Among
// pods of equal readiness, one not being deleted comes first.
func TestTheIndexAnswersTheNodesCurrentSidecar(t *testing.T) {
	cases := []struct {
		name   string
		events func(index *sidecarIndex)
		want   string
		ready  bool
	}{
		{name: "the old pod is marked for deletion after the new one is ready", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("old", true, false))
			index.hold(sidecarNamed("new", true, false))
			index.hold(sidecarNamed("old", true, true))
		}, want: "new", ready: true},
		{name: "the old pod is deleted after the new one is ready", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("old", true, false))
			index.hold(sidecarNamed("new", true, false))
			index.hold(sidecarNamed("old", false, true))
			index.drop(sidecarNamed("old", false, true))
		}, want: "new", ready: true},
		{name: "a ready pod before one that is starting", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("old", true, false))
			index.hold(sidecarNamed("new", false, false))
		}, want: "old", ready: true},
		{name: "a ready pod being deleted before one that is starting", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("new", false, false))
			index.hold(sidecarNamed("old", true, true))
		}, want: "old", ready: true},
		{name: "a starting pod before one being deleted that is not ready", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("new", false, false))
			index.hold(sidecarNamed("old", false, true))
		}, want: "new", ready: false},
		{name: "a pod being deleted when it is the only one", events: func(index *sidecarIndex) {
			index.hold(sidecarNamed("old", false, true))
		}, want: "old", ready: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			index := newSidecarIndex()
			c.events(index)

			held, there := index.on("node-1")
			if !there || held.Name != c.want || held.Ready != c.ready {
				t.Errorf("the index answers %+v (held: %v), want %s with ready=%v", held, there, c.want, c.ready)
			}
		})
	}
}
