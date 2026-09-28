package kubernetes

// These tests cover the resource operations against small API
// servers: listing, reading, publishing status, patching, and
// evicting.

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/cluster"
	"github.com/liken-sh/liken/machine"
)

func TestListMachinesReadsTheCollection(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind": "MachineList",
			"items": []machine.Machine{
				{Kind: "Machine", Metadata: api.ObjectMeta{Name: "node-1"}},
				{Kind: "Machine", Metadata: api.ObjectMeta{Name: "node-2"}},
			},
		})
	}))
	machines, err := ListMachines(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 2 || machines[1].Metadata.Name != "node-2" {
		t.Errorf("got %+v", machines)
	}
}

func TestGetClusterReadsOneCluster(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := ClustersPath + "/lab"; r.URL.Path != want {
			t.Errorf("got %s, want %s", r.URL.Path, want)
		}
		_ = json.NewEncoder(w).Encode(&cluster.Cluster{
			Kind:     "Cluster",
			Metadata: api.ObjectMeta{Name: "lab"},
		})
	}))
	clusterDoc, err := GetCluster(client, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if clusterDoc.Metadata.Name != "lab" {
		t.Errorf("got %q", clusterDoc.Metadata.Name)
	}
}

func TestPublishStatusWritesTheStatusSubresource(t *testing.T) {
	var path string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}}
	if _, err := PublishStatus(client, m, &machine.MachineStatus{Phase: api.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	if want := MachinesPath + "/node-1/status"; path != want {
		t.Errorf("status goes through the subresource, got %s", path)
	}
}

func TestPublishClusterStatusWritesTheStatusSubresource(t *testing.T) {
	var path string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
	}))
	clusterDoc := &cluster.Cluster{Metadata: api.ObjectMeta{Name: "lab"}}
	if _, err := PublishClusterStatus(client, clusterDoc); err != nil {
		t.Fatal(err)
	}
	if want := ClustersPath + "/lab/status"; path != want {
		t.Errorf("status goes through the subresource, got %s", path)
	}
}

func TestPatchJSONSendsAMergePatch(t *testing.T) {
	var contentType, body string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
	}))
	if err := client.PatchJSON("/api/v1/nodes/node-1", []byte(`{"spec":{"unschedulable":true}}`)); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/merge-patch+json" {
		t.Errorf("a merge patch declares itself: %s", contentType)
	}
	if body != `{"spec":{"unschedulable":true}}` {
		t.Errorf("got %s", body)
	}
}

func TestPatchJSONCarriesTheServersRefusal(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nodes is forbidden", http.StatusForbidden)
	}))
	if err := client.PatchJSON("/api/v1/nodes/node-1", []byte(`{}`)); err == nil {
		t.Error("a refused patch is an error")
	}
}

// A status write answers the resourceVersion the API server gave it,
// and an answer with no body answers no version.
func TestPublishStatusAnswersTheWrittenVersion(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{"an answer with the object", `{"metadata":{"name":"node-1","resourceVersion":"42"}}`, "42"},
		{"an answer with no body", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(c.answer))
			}))
			m := &machine.Machine{Metadata: api.ObjectMeta{Name: "node-1"}}
			version, err := PublishStatus(client, m, &machine.MachineStatus{})
			if err != nil || version != c.want {
				t.Errorf("PublishStatus = %q, %v; want %q", version, err, c.want)
			}
		})
	}
}
