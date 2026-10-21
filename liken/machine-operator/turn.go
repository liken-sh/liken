package main

// The turn: whether the rollout conductor granted this machine its
// reboot turn.
//
// The grant is the machine's entry in the Cluster's status.rebootTurns
// (cluster-operator/rollout.go). The conductor adds the entry in a
// write conditional on the Cluster's resourceVersion, after it checked
// every budget in the same write, so the entry is the budget's own
// record. A condition on the Machine could land after the turn it
// reports had ended, from a conductor that paused before it wrote; an
// entry cannot, because the conductor removes it in the same kind of
// conditional write.
//
// The pass acts on an entry only after it read the Cluster from the API
// server, because the watch's copy can be older than the conductor's
// last write, and an entry the conductor removed must not start a
// drain. A copy that holds no entry answers alone: a copy older than a
// new entry costs the machine one pass, and the conductor's grant
// write to the Machine wakes that pass. So a settled fleet sends no
// read. A read that fails grants nothing, and the machine waits for
// the next pass.

import (
	"fmt"
	"slices"

	"github.com/liken-sh/liken/liken/cluster"
	"github.com/liken-sh/liken/liken/kubernetes"
)

// holdsTurn reports whether the Cluster's ledger holds an entry for
// this machine.
func holdsTurn(r *reader, clusterName, machineName string) bool {
	held, err := r.cluster(clusterName)
	if err != nil || !hasEntry(held, machineName) {
		return false
	}
	fresh, err := kubernetes.GetCluster(r.client, clusterName)
	if err != nil {
		fmt.Printf("reading the Cluster for this machine's reboot turn: %v\n", err)
		return false
	}
	return hasEntry(fresh, machineName)
}

func hasEntry(c *cluster.Cluster, machineName string) bool {
	return slices.ContainsFunc(c.Status.RebootTurns, func(turn cluster.RebootTurn) bool { return turn.Machine == machineName })
}
