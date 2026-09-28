package kubernetes

// These tests cover eviction and pod ownership, the operations the
// drain and the pod steward share.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCompletedReadsThePhase(t *testing.T) {
	cases := []struct {
		phase string
		want  bool
	}{
		{"Succeeded", true},
		{"Failed", true},
		{"Running", false},
		{"Pending", false},
	}
	for _, c := range cases {
		t.Run(c.phase, func(t *testing.T) {
			p := Pod{Status: PodStatus{Phase: c.phase}}
			if got := p.Completed(); got != c.want {
				t.Errorf("a %s pod: Completed() = %v, want %v", c.phase, got, c.want)
			}
		})
	}
}

func TestIsDaemonReadsOwnership(t *testing.T) {
	daemon := Pod{Metadata: PodMetadata{
		OwnerReferences: []OwnerReference{{Kind: "DaemonSet"}},
	}}
	if !daemon.IsDaemon() {
		t.Error("a DaemonSet-owned pod is a daemon")
	}
	replica := Pod{Metadata: PodMetadata{
		OwnerReferences: []OwnerReference{{Kind: "ReplicaSet"}},
	}}
	if replica.IsDaemon() {
		t.Error("a ReplicaSet-owned pod is not")
	}
	if (&Pod{}).IsDaemon() {
		t.Error("an unowned pod is not")
	}
}

func TestEvictPodPostsTheEvictionSubresource(t *testing.T) {
	var path string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	p := Pod{Metadata: PodMetadata{Name: "web", Namespace: "default"}}
	if err := EvictPod(client, p); err != nil {
		t.Fatal(err)
	}
	if want := "/api/v1/namespaces/default/pods/web/eviction"; path != want {
		t.Errorf("got %s", path)
	}
}

func TestEvictPodCarriesTheServersRefusal(t *testing.T) {
	// A 429 status is how the Eviction API reports that a
	// PodDisruptionBudget would be violated. The caller gets an
	// error, and asks again later.
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Cannot evict pod as it would violate the pod's disruption budget.",
			http.StatusTooManyRequests)
	}))
	p := Pod{Metadata: PodMetadata{Name: "web", Namespace: "default"}}
	if err := EvictPod(client, p); err == nil {
		t.Error("a refused eviction is an error")
	}
}

func TestListPodsOnNodeAsksTheServerToFilter(t *testing.T) {
	var query string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "PodList",
			"items": []Pod{
				{Metadata: PodMetadata{Name: "web", Namespace: "default"}},
			},
		})
	}))
	pods, err := ListPodsOnNode(client, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Metadata.Name != "web" {
		t.Errorf("got %+v", pods)
	}
	if want := "fieldSelector=spec.nodeName%3Dnode-1"; query != want {
		t.Errorf("the server does the filtering, so the query carries the selector: %s", query)
	}
}
