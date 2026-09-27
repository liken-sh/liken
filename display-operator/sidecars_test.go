package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// The loop lists and then watches from the listing's version, and the
// watch's events move the index.
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
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	go index.run(ctx, NewClient(api.URL, api.Client(), ""), sidecarNamespace)

	eventually(t, "the watch's new pod", func() bool {
		pod, held := index.on("node-2")
		return held && pod.IP == "10.42.1.3"
	})
	eventually(t, "the watch's deletion", func() bool {
		_, held := index.on("node-1")
		return !held
	})
}

// sidecarAPI serves the capture sidecars' listing at version 41 and
// answers each watch with the next of answers, counting the listings
// and recording the version each watch asked for. A watch past the
// last answer is held open until the loop ends.
type sidecarAPI struct {
	mu      sync.Mutex
	lists   int
	asked   []string
	answers []func(w http.ResponseWriter)
}

func (a *sidecarAPI) serve(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "true" {
			a.mu.Lock()
			a.lists++
			a.mu.Unlock()
			fmt.Fprint(w, `{"metadata":{"resourceVersion":"41"},"items":[]}`)
			return
		}
		a.mu.Lock()
		a.asked = append(a.asked, r.URL.Query().Get("resourceVersion"))
		n := len(a.asked)
		a.mu.Unlock()
		if n > len(a.answers) {
			<-r.Context().Done()
			return
		}
		a.answers[n-1](w)
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, server.Client(), "")
}

func (a *sidecarAPI) run(t *testing.T, window time.Duration) (int, []string) {
	t.Helper()
	client := a.serve(t)
	ctx, stop := context.WithTimeout(t.Context(), window)
	defer stop()
	newSidecarIndex().run(ctx, client, sidecarNamespace)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lists, append([]string(nil), a.asked...)
}

const sidecarAddedAt42 = `{"type":"ADDED","object":{"metadata":{"name":"display-operator-b","namespace":"liken-system","resourceVersion":"42"},"spec":{"nodeName":"node-2"}}}`

// A watch that lived a second and ended resumes at the last version it
// delivered, with no second listing, whether the API server ended it
// cleanly or the connection was reset.
func TestTheSidecarWatchResumesWhereItEnded(t *testing.T) {
	cases := []struct {
		name  string
		reset bool
	}{
		{name: "ended cleanly"},
		{name: "reset", reset: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &sidecarAPI{answers: []func(w http.ResponseWriter){func(w http.ResponseWriter) {
				fmt.Fprint(w, sidecarAddedAt42)
				w.(http.Flusher).Flush()
				time.Sleep(1100 * time.Millisecond)
				if c.reset {
					if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
						_ = conn.Close()
					}
				}
			}}}

			lists, asked := api.run(t, 1600*time.Millisecond)

			if lists != 1 || len(asked) != 2 || asked[1] != "42" {
				t.Errorf("the loop listed %d times and watched from %v, want one listing and a second watch from 42",
					lists, asked)
			}
		})
	}
}

// A 410 Gone means the version is too old, so the loop lists again at
// once and watches from the new listing.
func TestTheSidecarWatchListsAgainOnGone(t *testing.T) {
	api := &sidecarAPI{answers: []func(w http.ResponseWriter){func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"type":"ERROR","object":{"kind":"Status","code":410}}`)
	}}}

	lists, _ := api.run(t, 300*time.Millisecond)

	if lists != 2 {
		t.Errorf("the loop listed %d times, want a second listing at once after the 410", lists)
	}
}

// A watch that lived under a second is a failure, whatever it
// delivered, so the loop waits before the next one.
func TestTheSidecarWatchWaitsAfterAShortWatch(t *testing.T) {
	answer := func(w http.ResponseWriter) { fmt.Fprint(w, sidecarAddedAt42) }
	api := &sidecarAPI{answers: []func(w http.ResponseWriter){answer, answer, answer, answer, answer}}

	lists, asked := api.run(t, 500*time.Millisecond)

	if lists+len(asked) > 3 {
		t.Errorf("the loop listed %d times and watched %d times in half a second, want it to wait",
			lists, len(asked))
	}
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

// A listing replaces the index in one step, so a capture that reads the
// index while a listing lands never finds a node the listing names
// missing.
func TestAListingNeverEmptiesTheIndexOnTheWay(t *testing.T) {
	index := newSidecarIndex()
	pods := []Pod{readySidecar("node-1", "10.42.0.7")}
	index.replace(pods)
	var missed atomic.Int64
	ctx, stop := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer stop()
	go func() {
		for ctx.Err() == nil {
			index.replace(pods)
		}
	}()

	for ctx.Err() == nil {
		if _, held := index.on("node-1"); !held {
			missed.Add(1)
		}
	}

	if n := missed.Load(); n > 0 {
		t.Errorf("a read found node-1 missing %d times while listings landed", n)
	}
}
