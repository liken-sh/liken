package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// noticesPodJSON is JSON, not a struct, so the tests check that the
// wire field names decode into the fields this program reads.
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

func TestGetPodReadsOneClaimHolder(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/front-desk/pods/notices-7d9f" {
			t.Errorf("the operator read %s, want the notices pod", r.URL.Path)
		}
		fmt.Fprint(w, noticesPodJSON)
	}))

	pod, err := getPod(client, "front-desk", "notices-7d9f")
	if err != nil {
		t.Fatalf("reading the pod front-desk/notices-7d9f: %v", err)
	}
	if pod.Metadata.key() != "front-desk/notices-7d9f" {
		t.Errorf("the pod is %q, want front-desk/notices-7d9f", pod.Metadata.key())
	}
	if pod.Metadata.Labels["panel"] != "notices" {
		t.Errorf("the pod has %v, want panel=notices", pod.Metadata.Labels)
	}
}

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
		t.Fatalf("listing the pods of node liken-1: %v", err)
	}
	if len(pods) != 1 || pods[0].Metadata.Name != "notices-7d9f" {
		t.Errorf("the listing held %+v, want the notices pod", pods)
	}
}

// The watch reopens its connection after each failure, so a shutdown
// must end it through the context and not wait for the next retry.
func TestThePodWatchEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	watcher := testWatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))

	<-openPods(ctx, watcher, "liken-1", func() {}, nil).Done()
}
