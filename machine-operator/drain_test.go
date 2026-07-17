package main

// The drain's decision table: what gets evicted, when the node is
// considered clear, and who is allowed to uncordon.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/liken/kubernetes"
)

var drainNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func pod(name, ns string, opts ...func(*kubernetes.Pod)) kubernetes.Pod {
	var p kubernetes.Pod
	p.Metadata.Name = name
	p.Metadata.Namespace = ns
	p.Status.Phase = "Running"
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

func ownedByDaemonSet(p *kubernetes.Pod) {
	p.Metadata.OwnerReferences = []kubernetes.OwnerReference{{Kind: "DaemonSet"}}
}

func mirror(p *kubernetes.Pod) {
	p.Metadata.Annotations = map[string]string{mirrorPodAnnotation: "true"}
}

func completed(p *kubernetes.Pod) {
	p.Status.Phase = "Succeeded"
}

func TestEvictablePodsSkipsWhatCannotMove(t *testing.T) {
	pods := []kubernetes.Pod{
		pod("web", "default"),
		pod("liken-operator", "liken-system", ownedByDaemonSet),
		pod("etcd-shim", "kube-system", mirror),
		pod("job-done", "default", completed),
	}
	evictable := evictablePods(pods)
	if len(evictable) != 1 || evictable[0].Metadata.Name != "web" {
		t.Errorf("only the ordinary workload moves: %+v", evictable)
	}
}

// drainNode builds a Node in a given cordon state; since is the
// draining-since annotation, "" for none.
func drainNode(unschedulable bool, cordonedByUs bool, since string) *nodeObject {
	n := &nodeObject{}
	n.Metadata.Name = "node-4"
	n.Spec.Unschedulable = unschedulable
	n.Metadata.Annotations = map[string]string{}
	if cordonedByUs {
		n.Metadata.Annotations[cordonedAnnotation] = "true"
	}
	if since != "" {
		n.Metadata.Annotations[drainingSinceAnnotation] = since
	}
	return n
}

func TestDrainBeginsByCordoning(t *testing.T) {
	step := decideDrainStep(drainNode(false, false, ""), []kubernetes.Pod{pod("web", "default")}, drainNow)
	if step.patch == nil {
		t.Fatal("an uncordoned node needs the cordon patch")
	}
	if !strings.Contains(string(step.patch), `"unschedulable":true`) {
		t.Errorf("the patch sets the cordon: %s", step.patch)
	}
	if !strings.Contains(string(step.patch), cordonedAnnotation) {
		t.Errorf("the patch claims the cordon as ours: %s", step.patch)
	}
	if step.clear {
		t.Error("a pod still runs here; the reboot waits")
	}
	if len(step.evict) != 1 {
		t.Errorf("the same pass starts evicting: %+v", step.evict)
	}
}

func TestDrainRespectsAHumanCordon(t *testing.T) {
	// The node was already unschedulable when the drain arrived: some
	// human cordoned it. The drain records its deadline anchor but does
	// not claim the cordon, so it will never uncordon what it didn't
	// cordon.
	step := decideDrainStep(drainNode(true, false, ""), nil, drainNow)
	if step.patch == nil || strings.Contains(string(step.patch), cordonedAnnotation) {
		t.Errorf("the cordon belongs to whoever set it: %s", step.patch)
	}
}

func TestDrainIsClearWhenNothingEvictableRemains(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since),
		[]kubernetes.Pod{pod("liken-operator", "liken-system", ownedByDaemonSet)}, drainNow)
	if step.patch != nil {
		t.Errorf("already cordoned and anchored: %s", step.patch)
	}
	if !step.clear {
		t.Error("nothing left to move; the reboot may proceed")
	}
}

func TestDrainForcesThroughAfterTheDeadline(t *testing.T) {
	since := drainNow.Add(-10 * time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []kubernetes.Pod{pod("stubborn", "default")}, drainNow)
	if !step.clear {
		t.Error("past the deadline the reboot proceeds; the pod dies with the machine")
	}
	if len(step.evict) != 0 {
		t.Errorf("no point evicting what the reboot is about to take: %+v", step.evict)
	}
}

func TestDrainKeepsEvictingBeforeTheDeadline(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []kubernetes.Pod{pod("web", "default")}, drainNow)
	if step.clear {
		t.Error("a movable pod holds the reboot")
	}
	if len(step.evict) != 1 {
		t.Errorf("got %+v", step.evict)
	}
}

func TestUncordonOnlyTakesBackOurOwnCordon(t *testing.T) {
	if !decideUncordon(drainNode(true, true, "")) {
		t.Error("our cordon, our job to remove")
	}
	if decideUncordon(drainNode(true, false, "")) {
		t.Error("a human's cordon is not ours to remove")
	}
	if decideUncordon(drainNode(false, false, "")) {
		t.Error("nothing to do on an uncordoned node")
	}
}

// drainAPI is a miniature API server recording pod listings, eviction
// posts, and node patches, and it can be told to fail the listing or
// the patch, the two reads and writes a drain step depends on.
type drainAPI struct {
	pods      []kubernetes.Pod
	listFail  bool
	patchFail bool
	evictions []string
	patched   string
}

func (fake *drainAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			if fake.listFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": fake.pods})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eviction"):
			fake.evictions = append(fake.evictions, r.URL.Path)
		case r.Method == http.MethodPatch:
			if fake.patchFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			body := make([]byte, 4096)
			n, _ := r.Body.Read(body)
			fake.patched = string(body[:n])
		}
	})
}

// rebootingConvergence is the decision the drain gates: a granted
// reboot, ready to write its intent.
func rebootingConvergence() convergence {
	return convergence{
		condition:     notConverged("SpecConverged", "RebootRequested", "reboot requested to apply the staged spec"),
		requestReboot: true,
	}
}

func TestGateThroughDrainHoldsWhenPodsCannotBeListed(t *testing.T) {
	fake := &drainAPI{listFail: true}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(client, drainNode(true, true, since), rebootingConvergence(), drainNow)
	if conv.requestReboot {
		t.Error("a node whose pods can't be listed can't be judged clear; the reboot holds")
	}
	if conv.condition.Reason != "Draining" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestGateThroughDrainHoldsWhenTheCordonFails(t *testing.T) {
	fake := &drainAPI{patchFail: true}
	client := testClient(t, fake.handler())
	conv := gateThroughDrain(client, drainNode(false, false, ""), rebootingConvergence(), drainNow)
	if conv.requestReboot {
		t.Error("an uncordoned node must not reboot; new pods could still land on it")
	}
	if conv.condition.Reason != "Draining" {
		t.Errorf("got %+v", conv.condition)
	}
}

func TestGateThroughDrainEvictsAndHolds(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(client, drainNode(true, true, since), rebootingConvergence(), drainNow)
	if conv.requestReboot {
		t.Error("a movable pod holds the reboot")
	}
	want := "/api/v1/namespaces/default/pods/web/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("the same pass asks the pod to leave: %v", fake.evictions)
	}
	if conv.condition.Reason != "Draining" || !strings.Contains(conv.condition.Message, "1 pods") {
		t.Errorf("the condition reports the drain's progress: %+v", conv.condition)
	}
}

func TestGateThroughDrainCordonsAnEmptyNodeAndReleases(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	conv := gateThroughDrain(client, drainNode(false, false, ""), rebootingConvergence(), drainNow)
	if !strings.Contains(fake.patched, `"unschedulable":true`) {
		t.Errorf("the first pass cordons: %s", fake.patched)
	}
	if !conv.requestReboot {
		t.Error("an empty node is clear the moment it is cordoned")
	}
}

func TestGateThroughDrainReleasesAClearNode(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("liken-operator", "liken-system", ownedByDaemonSet)}}
	client := testClient(t, fake.handler())
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	conv := gateThroughDrain(client, drainNode(true, true, since), rebootingConvergence(), drainNow)
	if !conv.requestReboot {
		t.Error("nothing left to move; the reboot proceeds")
	}
	if conv.condition.Reason != "RebootRequested" {
		t.Errorf("the decision's own condition survives the gate: %+v", conv.condition)
	}
}

func TestListPodsOnNodeFiltersByNodeName(t *testing.T) {
	fake := &drainAPI{pods: []kubernetes.Pod{pod("web", "default")}}
	client := testClient(t, fake.handler())
	pods, err := kubernetes.ListPodsOnNode(client, "node-4")
	if err != nil || len(pods) != 1 {
		t.Fatalf("got %v, %v", pods, err)
	}
}

func TestEvictPodPostsTheEvictionSubresource(t *testing.T) {
	fake := &drainAPI{}
	client := testClient(t, fake.handler())
	if err := kubernetes.EvictPod(client, pod("web", "default")); err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/namespaces/default/pods/web/eviction"
	if len(fake.evictions) != 1 || fake.evictions[0] != want {
		t.Errorf("got %v", fake.evictions)
	}
}
