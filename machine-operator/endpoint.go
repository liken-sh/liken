package main

import (
	"slices"

	"github.com/liken-sh/liken/cluster"
)

// localAPIEndpoint is the machine's own path to the API server,
// chosen over the service VIP the pod environment offers. The VIP is
// iptables NAT: each connection gets pinned to one API server, and
// when that server's machine dies silently, everything pinned to it
// stalls on timeouts — long enough for healthy machines' heartbeats
// to lapse and read as Lost. This pod runs on the host's network, so
// localhost is the machine, and the machine always has a better
// path: a leader runs an API server of its own on 6443, and a
// follower runs k3s's agent load balancer on 6444, which
// health-checks every server and fails over between them. These are
// the same endpoints the machine's own kubelet uses, so the
// operator's view of the API can never be worse than the kubelet's.
// A machine with no cluster document falls back to the environment's
// endpoint ("").
func localAPIEndpoint(clusterDoc *cluster.Cluster, name string) string {
	if clusterDoc == nil {
		return ""
	}
	if slices.Contains(clusterDoc.Spec.Leaders, name) {
		return "https://127.0.0.1:6443"
	}
	return "https://127.0.0.1:6444"
}
