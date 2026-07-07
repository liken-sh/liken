package main

// The drain's decision table: what gets evicted, when the node is
// considered clear, and who is allowed to uncordon.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

var drainNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func pod(name, ns string, opts ...func(*podObject)) podObject {
	var p podObject
	p.Metadata.Name = name
	p.Metadata.Namespace = ns
	p.Status.Phase = "Running"
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

func ownedByDaemonSet(p *podObject) {
	p.Metadata.OwnerReferences = []ownerReference{{Kind: "DaemonSet"}}
}

func mirror(p *podObject) {
	p.Metadata.Annotations = map[string]string{mirrorPodAnnotation: "true"}
}

func completed(p *podObject) {
	p.Status.Phase = "Succeeded"
}

func TestEvictablePodsSkipsWhatCannotMove(t *testing.T) {
	pods := []podObject{
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
	step := decideDrainStep(drainNode(false, false, ""), []podObject{pod("web", "default")}, drainNow)
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
		[]podObject{pod("liken-operator", "liken-system", ownedByDaemonSet)}, drainNow)
	if step.patch != nil {
		t.Errorf("already cordoned and anchored: %s", step.patch)
	}
	if !step.clear {
		t.Error("nothing left to move; the reboot may proceed")
	}
}

func TestDrainForcesThroughAfterTheDeadline(t *testing.T) {
	since := drainNow.Add(-10 * time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []podObject{pod("stubborn", "default")}, drainNow)
	if !step.clear {
		t.Error("past the deadline the reboot proceeds; the pod dies with the machine")
	}
	if len(step.evict) != 0 {
		t.Errorf("no point evicting what the reboot is about to take: %+v", step.evict)
	}
}

func TestDrainKeepsEvictingBeforeTheDeadline(t *testing.T) {
	since := drainNow.Add(-time.Minute).Format(time.RFC3339)
	step := decideDrainStep(drainNode(true, true, since), []podObject{pod("web", "default")}, drainNow)
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
// posts, and node patches.
type drainAPI struct {
	pods      []podObject
	evictions []string
	patched   string
}

func (api *drainAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": api.pods})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eviction"):
			api.evictions = append(api.evictions, r.URL.Path)
		case r.Method == http.MethodPatch:
			body := make([]byte, 4096)
			n, _ := r.Body.Read(body)
			api.patched = string(body[:n])
		}
	})
}

func TestListPodsOnNodeFiltersByNodeName(t *testing.T) {
	api := &drainAPI{pods: []podObject{pod("web", "default")}}
	client := testClient(t, api.handler())
	pods, err := listPodsOnNode(client, "node-4")
	if err != nil || len(pods) != 1 {
		t.Fatalf("got %v, %v", pods, err)
	}
}

func TestEvictPodPostsTheEvictionSubresource(t *testing.T) {
	api := &drainAPI{}
	client := testClient(t, api.handler())
	if err := evictPod(client, pod("web", "default")); err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/namespaces/default/pods/web/eviction"
	if len(api.evictions) != 1 || api.evictions[0] != want {
		t.Errorf("got %v", api.evictions)
	}
}
