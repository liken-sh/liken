package main

// The pod-freshness guard's pure judgment: whether this operator's
// own pod predates the release it is running.

import (
	"net/http"
	"testing"

	"github.com/liken-sh/liken/kubernetes"
)

func withOSVersion(version string) func(*kubernetes.Pod) {
	return func(p *kubernetes.Pod) {
		p.Metadata.Annotations = map[string]string{osVersionAnnotation: version}
	}
}

func TestDecidePodStaleWhenTheAnnotationPredatesTheRelease(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.01-001"))}
	if !decidePodStale(pods, "2026.08.12-002") {
		t.Error("a pod whose annotation names an older release is stale")
	}
}

func TestDecidePodStaleWhenTheAnnotationMatches(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.12-002"))}
	if decidePodStale(pods, "2026.08.12-002") {
		t.Error("a matching annotation is a current pod")
	}
}

func TestDecidePodStaleWithNoMatchingPod(t *testing.T) {
	if decidePodStale(nil, "2026.08.12-002") {
		t.Error("no pod to judge reads as current; a real fault must not hide behind a missing pod")
	}
}

func TestDecidePodStaleWithNoRunningVersion(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system", withOSVersion("2026.08.12-002"))}
	if decidePodStale(pods, "") {
		t.Error("facts with no version cannot support a stale verdict")
	}
}

func TestDecidePodStaleWithNoAnnotation(t *testing.T) {
	pods := []kubernetes.Pod{pod("op-1", "liken-system")}
	if decidePodStale(pods, "2026.08.12-002") {
		t.Error("a pod with no annotation reads as current")
	}
}

func TestOwnPodIsStaleReadsCurrentWhenTheListFails(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if ownPodIsStale(client, "node-1", "2026.08.12-002") {
		t.Error("a failed list must never manufacture a fault; it reads as current")
	}
}
