package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// The listing is the whole truth, so a pod the listing does not name
// is gone from the index.
func TestAListingReplacesWhatTheIndexHeld(t *testing.T) {
	index := newSidecarIndex()
	index.hold(readySidecar("node-1", "10.42.0.7"))
	index.replace([]Pod{readySidecar("node-2", "10.42.1.3")})

	if _, held := index.on("node-1"); held {
		t.Error("a node the listing did not name is still in the index")
	}
	if _, held := index.on("node-2"); !held {
		t.Error("a node the listing named is not in the index")
	}
}

// One session is a listing and then a watch, and the watch's events
// move the index with no second listing.
func TestTheIndexFollowsTheWatch(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.RawQuery, "watch=true") {
			fmt.Fprint(w, `{"metadata":{"resourceVersion":"41"},"items":[
				{"metadata":{"name":"display-operator-a","namespace":"liken-system"},
				 "spec":{"nodeName":"node-1"},
				 "status":{"podIP":"10.42.0.7","conditions":[{"type":"Ready","status":"True"}]}}]}`)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "resourceVersion=41") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"type":"ADDED","object":{"metadata":{"name":"display-operator-b","namespace":"liken-system"},
			"spec":{"nodeName":"node-2"},
			"status":{"podIP":"10.42.1.3","conditions":[{"type":"Ready","status":"True"}]}}}`)
		fmt.Fprint(w, `{"type":"DELETED","object":{"metadata":{"name":"display-operator-a","namespace":"liken-system"},
			"spec":{"nodeName":"node-1"}}}`)
	}))
	defer api.Close()

	index := newSidecarIndex()
	if err := index.session(context.Background(), NewClient(api.URL, api.Client(), ""), sidecarNamespace); err != nil {
		t.Fatal(err)
	}

	if pod, held := index.on("node-2"); !held || pod.IP != "10.42.1.3" {
		t.Errorf("the watch's new pod is %+v, want the one the event named", pod)
	}
	if _, held := index.on("node-1"); held {
		t.Error("the watch's deletion left the pod in the index")
	}
}
