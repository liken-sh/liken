package main

// The operator's access to its own Kubernetes Node object. The Node
// is the kubelet's account of this machine, and the operator reads
// and writes it for several jobs: mirroring its health onto the
// Machine (conditions.go), reconciling its labels (labels.go),
// cordoning and draining it ahead of a reboot (drain.go), and
// deleting it to finish a demotion (demotion.go).

import (
	"net/http"

	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// nodesPath is the core API's home for Node objects: no group, just
// a version, which is what "core" means in the URL scheme.
const nodesPath = "/api/v1/nodes"

// nodeObject is the sliver of a Kubernetes Node the operator needs:
// the labels, where a demoted machine's old role still shows; the
// conditions, where the kubelet's health shows (reconcile.go mirrors
// the Node's Ready condition onto the Machine); and the cordon
// state, meaning the unschedulable flag plus the annotations that
// record whether liken set it (drain.go).
type nodeObject struct {
	Metadata struct {
		Name        string            `json:"name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		Conditions []machine.Condition `json:"conditions"`
	} `json:"status"`
}

func getNode(c *kubernetes.Client, name string) (*nodeObject, error) {
	n := &nodeObject{}
	if err := c.RequestJSON(http.MethodGet, nodesPath+"/"+name, nil, n); err != nil {
		return nil, err
	}
	return n, nil
}

func deleteNode(c *kubernetes.Client, name string) error {
	return c.RequestJSON(http.MethodDelete, nodesPath+"/"+name, nil, nil)
}
