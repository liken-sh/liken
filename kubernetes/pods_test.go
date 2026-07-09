package kubernetes

// Eviction and pod ownership, the verbs the drain and the pod
// steward share.

import (
	"net/http"
	"testing"
)

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
