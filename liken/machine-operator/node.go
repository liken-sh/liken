package main

// The operator's access to the Kubernetes Node object of its own
// machine. The kubelet maintains that Node, and the operator reads and
// writes it to copy its health onto the Machine (conditions.go),
// reconcile its labels (labels.go) and taints (taints.go), cordon and
// drain it before a reboot (drain.go), and delete it to finish a
// demotion (demotion.go).

import (
	"encoding/json"
	"net/http"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
)

const nodesPath = "/api/v1/nodes"

// nodeObject is the part of a Node that the operator reads. The
// taints are written back as a whole list under the resourceVersion
// that was read (taints.go). The UID is the precondition of
// deleteNode and names the Node in the owner reference of the
// machine's ResourceSlice (dra.go).
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

// nodeTaint is one entry of the Node's spec.taints. Value has
// omitempty, as in the core/v1 Taint type.
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

// deleteNode deletes the Node only when it still has the UID that the
// caller read. When k3s registers the Node again under the same name
// after the read, the new Node has another UID, and the API server
// returns 409 Conflict and keeps it. Without the precondition, a pass
// that works from a stale copy could delete the Node that the machine
// just registered.
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
