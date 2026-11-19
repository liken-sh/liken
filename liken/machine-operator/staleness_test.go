package main

// Tests for decidePodStale and ownPodIsStale, which tell whether this
// operator's pod is older than the release that is running.

import (
	"net/http"
	"testing"

	"github.com/liken-sh/liken/liken/kubernetes"
)

func withOSVersion(version string) func(*kubernetes.Pod) {
	return func(p *kubernetes.Pod) {
		p.Metadata.Annotations = map[string]string{osVersionAnnotation: version}
	}
}

func TestDecidePodStaleWhenTheAnnotationIsOlderThanTheRelease(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.01-001"))}
	if !decidePodStale(pods, "2026.08.12-002") {
		t.Error("a pod whose annotation is an older release is stale")
	}
}

func TestDecidePodStaleWhenTheAnnotationMatches(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.12-002"))}
	if decidePodStale(pods, "2026.08.12-002") {
		t.Error("a pod whose annotation matches the release is current")
	}
}

func TestDecidePodStaleWithNoMatchingPod(t *testing.T) {
	if decidePodStale(nil, "2026.08.12-002") {
		t.Error("with no pod, the result is current, so a real fault is not hidden behind AwaitingPodRefresh")
	}
}

func TestDecidePodStaleWithNoRunningVersion(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.12-002"))}
	if decidePodStale(pods, "") {
		t.Error("with no running version, the result is current")
	}
}

func TestDecidePodStaleWithNoAnnotation(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system")}
	if decidePodStale(pods, "2026.08.12-002") {
		t.Error("a pod with no annotation is current")
	}
}

func TestOwnPodIsStaleIsFalseWhenTheListFails(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if ownPodIsStale(&reader{client: client}, "node-1", "2026.08.12-002") {
		t.Error("ownPodIsStale is true after a failed list, want false so that no fault is reported without evidence")
	}
}
