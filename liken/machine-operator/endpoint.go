package main

import (
	"slices"

	"github.com/liken-sh/liken/cluster"
)

// localAPIEndpoint returns the machine's own path to the API
// server, chosen instead of the service VIP that the pod environment
// offers. The VIP works through iptables NAT: each connection is
// pinned to one API server. When that server's machine dies
// silently, everything pinned to it stalls on timeouts, for long
// enough that healthy machines' heartbeats lapse and read as Lost.
// This pod runs on the host's network, so localhost reaches the
// machine, and the machine always has a better path. A leader runs
// an API server of its own on 6443, and a follower runs k3s's agent
// load balancer on 6444, which checks the health of every server and
// fails over between them. These are the same endpoints the
// machine's own kubelet uses, so the operator's view of the API can
// never be worse than the kubelet's. A machine with no cluster
// document falls back to the environment's endpoint ("").
func localAPIEndpoint(clusterDoc *cluster.Cluster, name string) string {
	if clusterDoc == nil {
		return ""
	}
	if slices.Contains(clusterDoc.Spec.Leaders, name) {
		return "https://127.0.0.1:6443"
	}
	return "https://127.0.0.1:6444"
}
