package main

// These tests cover the pod client: the read of one holder of a
// claim, the listing that is held to this node, and the watch that
// turns a label a person edited into a wake.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// A pod of the front desk, as the API server serves it. The fixture
// is JSON rather than a struct, because what these tests prove is
// that the field names on the wire reach the fields this program
// reads.
const noticesPodJSON = `{
  "apiVersion": "v1",
  "kind": "Pod",
  "metadata": {
    "name": "notices-7d9f",
    "namespace": "front-desk",
    "labels": {"panel": "notices"}
  },
  "spec": {"nodeName": "liken-1"}
}`

func TestGetPodReadsOneHolderOfAClaim(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/front-desk/pods/notices-7d9f" {
			t.Errorf("the operator read %s, want the notices pod", r.URL.Path)
		}
		fmt.Fprint(w, noticesPodJSON)
	}))

	pod, err := getPod(client, "front-desk", "notices-7d9f")
	if err != nil {
		t.Fatal(err)
	}
	if pod.Metadata.key() != "front-desk/notices-7d9f" {
		t.Errorf("the pod is %q, want front-desk/notices-7d9f", pod.Metadata.key())
	}
	if pod.Metadata.Labels["panel"] != "notices" {
		t.Errorf("the pod carries %v, want panel=notices", pod.Metadata.Labels)
	}
}

// The field selector is the whole of the narrowing. A screen's claim
// is held by a pod on the screen's own node, so the pods of every
// other node answer nothing a pass reads.
func TestListPodsReadsOnlyThisNodesPods(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PodsPath {
			t.Errorf("the operator read %s, want the pod collection", r.URL.Path)
		}
		if selector := r.URL.Query().Get("fieldSelector"); selector != "spec.nodeName=liken-1" {
			t.Errorf("the operator selected %q, want spec.nodeName=liken-1", selector)
		}
		fmt.Fprintf(w, `{"items":[%s]}`, noticesPodJSON)
	}))

	pods, err := listPods(client, "liken-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Metadata.Name != "notices-7d9f" {
		t.Errorf("the listing held %+v, want the notices pod", pods)
	}
}

// The watch turns each event into one wake, which is all the pass
// needs from it: the pass reads every pod that holds a claim again.
func TestThePodWatchWakesOnEveryEvent(t *testing.T) {
	events := 2
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			t.Errorf("the operator opened %s, want a watch", r.URL)
		}
		if selector := r.URL.Query().Get("fieldSelector"); selector != "spec.nodeName=liken-1" {
			t.Errorf("the watch selected %q, want spec.nodeName=liken-1", selector)
		}
		for i := range events {
			fmt.Fprintf(w, `{"type":"MODIFIED","object":{"metadata":{"name":"notices-%d"}}}`, i)
		}
	}))

	wakes := 0
	if err := streamPods(t.Context(), client, "liken-1", func() { wakes++ }); err != nil {
		t.Fatal(err)
	}
	if wakes != events {
		t.Errorf("the watch woke the loop %d times, want %d", wakes, events)
	}
}

// The watch reopens its connection until the context ends, and a
// context that ended is what stops it. Without that, a shutdown would
// wait out the retry.
func TestThePodWatchEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))

	watchPods(ctx, client, "liken-1", func() {})
}
