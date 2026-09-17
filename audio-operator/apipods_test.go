package main

import (
	"testing"
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
