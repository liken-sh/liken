package kubernetes

// Reading a ResourceClaim's allocation.
//
// When the kubelet asks the DRA driver to prepare a claim, the
// request names the claim but not what was allocated to it — the
// allocation lives on the claim's status, written by the scheduler,
// and the driver is expected to read it back from the API server.
// That is deliberate on Kubernetes' part: the claim object is the
// one source of truth for what a pod was granted, so a stale or
// replayed prepare call can never deliver anything but what the
// scheduler actually decided.
//
// The honest subset here is the read path only: liken never writes
// claims (workloads create them, the scheduler allocates them), so
// these types carry exactly the fields the driver reads.

import "net/http"

// ResourceClaim is the sliver the driver needs: which devices from
// which driver's pools were allocated.
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

// AllocatedDevice is one allocation result: the scheduler chose
// Device from Pool, published by Driver, to satisfy the claim's
// named Request. Pool and Device correspond to what the inventory
// published (resourceslices.go); Driver says whose inventory, which
// matters because one claim can mix devices from several drivers.
type AllocatedDevice struct {
	Request string `json:"request"`
	Driver  string `json:"driver"`
	Pool    string `json:"pool"`
	Device  string `json:"device"`
}

// GetResourceClaim reads one claim. Claims are namespaced — they
// belong to the workload that made them — so the path carries the
// namespace, unlike every other resource this package touches.
func GetResourceClaim(c *Client, namespace, name string) (*ResourceClaim, error) {
	path := "/apis/resource.k8s.io/v1/namespaces/" + namespace + "/resourceclaims/" + name
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, path, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}
