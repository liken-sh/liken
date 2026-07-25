package kubernetes

// This file reads Services for one purpose: to count the Services of
// type LoadBalancer that the cluster still holds.
//
// A LoadBalancer Service is served by a cloud controller, which
// assigns the address and holds the Service's
// service.kubernetes.io/load-balancer-cleanup finalizer. The
// finalizer keeps the Service in place until the controller releases
// the address and clears the finalizer, and no other component
// clears it. Where the controller has stopped, a deleted
// LoadBalancer Service therefore never finishes deleting. The
// machine operator counts these Services before the feature that
// carries the controller may stop (machine-operator/retraction.go).

// Service holds the part of a Kubernetes Service that liken reads:
// where the Service lives, and which kind of Service it is. Ports and
// selectors stay out of this type. The type alone states whether
// anything outside the cluster's own networking serves the Service.
type Service struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Type string `json:"type"`
	} `json:"spec"`
}

// ListLoadBalancerServices reads every Service in the cluster and
// keeps the ones of type LoadBalancer. The filter runs here rather
// than at the server, because a field selector can name only the
// fields the API server indexes, and spec.type is not one of them.
// The whole collection is cheap to read anyway: a Service is a
// per-workload object, not a per-pod one.
func ListLoadBalancerServices(c *Client) ([]Service, error) {
	services, err := List[Service](c, "/api/v1/services")
	if err != nil {
		return nil, err
	}
	var balanced []Service
	for _, s := range services {
		if s.Spec.Type == "LoadBalancer" {
			balanced = append(balanced, s)
		}
	}
	return balanced, nil
}
