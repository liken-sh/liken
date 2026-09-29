package main

// The operator's access to its own Kubernetes Node object. The Node
// is the kubelet's record of this machine, and the operator reads
// and writes it for several jobs: mirroring its health onto the
// Machine (conditions.go), reconciling its labels (labels.go),
// reconciling its taints (taints.go), cordoning and draining it ahead
// of a reboot (drain.go), and deleting it to finish a demotion
// (demotion.go).

import (
	"encoding/json"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
)

// nodesPath is the core API's home for Node objects: no group, just
// a version, which is what "core" means in the URL scheme.
const nodesPath = "/api/v1/nodes"

// nodeObject holds the small part of a Kubernetes Node that the
// operator needs: the labels, where a demoted machine's old role
// still shows; the conditions, where the kubelet's health shows
// (reconcile.go mirrors the Node's Ready condition onto the
// Machine); the cordon state, meaning the unschedulable flag plus
// the annotations that record whether liken set it (drain.go); the
// taints, which the operator rewrites as a whole list under the
// resourceVersion it read (taints.go); and the UID, which ties the
// device inventory's owner reference to this instance of the node
// (dra.go).
type nodeObject struct {
	Metadata struct {
		Name            string            `json:"name"`
		UID             string            `json:"uid"`
		ResourceVersion string            `json:"resourceVersion"`
		Labels          map[string]string `json:"labels"`
		Annotations     map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Unschedulable bool        `json:"unschedulable"`
		Taints        []nodeTaint `json:"taints"`
	} `json:"spec"`
	Status struct {
		Conditions []api.Condition `json:"conditions"`
	} `json:"status"`
}

// nodeTaint is one entry of the Node's spec.taints, in the Node's own
// shape rather than the Machine spec's. The value carries omitempty,
// because a taint with no value stores no value field at all, and a
// written empty string would be a value the API server keeps.
type nodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

func getNode(c *apiclient.Client, name string) (*nodeObject, error) {
	n := &nodeObject{}
	if err := c.RequestJSON(http.MethodGet, nodesPath+"/"+name, nil, n); err != nil {
		return nil, err
	}
	return n, nil
}

// deleteNode deletes the Node instance whose UID the caller read. The
// UID is a precondition of the delete: a Node that k3s registered again
// under the same name after the read has another UID, and the API
// server answers 409 and keeps it. Without the precondition, a pass
// that acted on a stale copy could delete the Node a machine just
// registered.
func deleteNode(c *apiclient.Client, name, uid string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion":    "v1",
		"kind":          "DeleteOptions",
		"preconditions": map[string]string{"uid": uid},
	})
	if err != nil {
		return err
	}
	return c.RequestJSON(http.MethodDelete, nodesPath+"/"+name, body, nil)
}
