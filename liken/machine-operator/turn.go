package main

import (
	"fmt"
	"slices"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
)

// holdsTurn reports whether this machine has its reboot turn, which
// is an entry for it in the Cluster's status.rebootTurns
// (cluster-operator/rollout.go). cluster-operator adds and removes
// each entry with a status write that is conditional on the
// Cluster's resourceVersion, so an entry is never stale. A
// condition on the Machine can be stale, because a cluster-operator
// that paused before it wrote the condition could write it after the
// turn had ended.
//
// The watch's copy of the Cluster can be older than cluster-operator's
// last write, so holdsTurn reads the Cluster from the API server
// before it returns true. A drain never starts for an entry that
// cluster-operator already removed. When the watch's copy has no entry,
// holdsTurn returns false without a read, so a fleet with no reboots
// in progress sends no reads. If that copy missed a new entry,
// cluster-operator's RebootApproved write to the Machine starts another pass.
func holdsTurn(r *reader, clusterName, machineName string) bool {
	held, err := r.cluster(clusterName)
	if err != nil || !hasEntry(held, machineName) {
		return false
	}
	fresh, err := kubernetes.GetCluster(r.client, clusterName)
	if err != nil {
		fmt.Printf("reading Cluster %s for the reboot turn of Machine %s: %v\n", clusterName, machineName, err)
		return false
	}
	return hasEntry(fresh, machineName)
}

func hasEntry(c *cluster.Cluster, machineName string) bool {
	return slices.ContainsFunc(c.Status.RebootTurns, func(turn cluster.RebootTurn) bool { return turn.Machine == machineName })
}
