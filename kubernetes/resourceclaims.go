package kubernetes

// This file reads a ResourceClaim's allocation.
//
// When the kubelet asks the DRA driver to prepare a claim, the
// request names the claim, but not what was allocated to it. The
// allocation lives in the claim's status, written by the scheduler,
// and the driver must read it back from the API server. Kubernetes
// designed it this way on purpose: the claim object is the one
// source of truth for what a pod was granted, so a stale or replayed
// prepare call can never deliver anything except what the scheduler
// actually decided.
//
// This file covers only the read path: liken never writes claims.
// Workloads create claims, and the scheduler allocates them. Because
// of this, these types carry only the fields that the driver reads.

import "net/http"

// ResourceClaim holds the part of the claim that the driver needs:
// which devices were allocated from which driver's pools.
type ResourceClaim struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		UID       string `json:"uid"`
	} `json:"metadata"`
	Status struct {
		Allocation *struct {
			Devices struct {
				Results []AllocatedDevice `json:"results"`
			} `json:"devices"`
		} `json:"allocation"`
	} `json:"status"`
}

// AllocatedDevice is one allocation result. The scheduler chose
// Device from Pool, published by Driver, to satisfy the claim's
// named Request. Pool and Device correspond to what the inventory
// published (see resourceslices.go). Driver names whose inventory
// this is, which matters because one claim can mix devices from
// several drivers.
type AllocatedDevice struct {
	Request string `json:"request"`
	Driver  string `json:"driver"`
	Pool    string `json:"pool"`
	Device  string `json:"device"`
}

// GetResourceClaim reads one claim. Claims are namespaced: each claim
// belongs to the workload that created it. Because of this, the path
// carries the namespace, unlike every other resource this package
// touches.
func GetResourceClaim(c *Client, namespace, name string) (*ResourceClaim, error) {
	path := "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, path, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}
