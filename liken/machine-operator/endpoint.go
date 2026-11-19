package main

import (
	"slices"

	"github.com/liken-sh/liken/liken/cluster"
)

// localAPIEndpoint returns the API server URL that the operator uses
// in place of the service VIP from the pod's environment. With no
// cluster document it returns "", and the caller uses the VIP. The VIP
// goes through iptables NAT, which pins each connection to one API
// server. When that server's machine dies without closing its
// connections, each pinned connection waits for a timeout, and that
// can last long enough for the heartbeats of healthy machines to lapse
// and for them to be reported Lost.
//
// The pod runs on the host network, so localhost is the machine
// itself. A leader runs an API server on 6443. A follower runs the k3s
// agent's load balancer on 6444, which checks the health of every API
// server and fails over between them. The machine's kubelet uses the
// same endpoints.
func localAPIEndpoint(clusterDoc *cluster.Cluster, name string) string {
	if clusterDoc == nil {
		return ""
	}
	if slices.Contains(clusterDoc.Spec.Leaders, name) {
		return "https://127.0.0.1:6443"
	}
	return "https://127.0.0.1:6444"
}
