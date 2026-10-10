package main

// Leader election: only one copy of this program acts on the fleet.
//
// The sweep writes Lost verdicts, the Cluster's status, and reboot
// grants, and it evicts and deletes. Two copies that sweep at the same
// time can each grant a reboot turn from a different view of the
// fleet, and together exceed the disruption budget. A rolling update, a
// node partition, and a replica count above one each run a second
// copy, so this program elects one acting copy through
// kubernetes/election, on a coordination.k8s.io Lease named
// liken-cluster-operator in liken-system. That package explains the
// election, its timings, and its limits.
//
// The election is not a fence. A former leader that paused can still
// land a grant it sent before the pause, after another copy took the
// Lease. The grant ledger in the Cluster's status, not the election,
// keeps the disruption budget against that grant (rollout.go).
//
// Only the copy that holds the Lease opens its watches and sweeps. A
// copy that the API server refuses on the Lease waits, as for any other
// failure of the election. The OS images carry this binary, so a
// rollback runs it under the RBAC that the target release's manifests
// grant. The Cluster CRD refuses a target older than 2026.09.28-001, the
// first release whose RBAC grants this program the create and update of
// its Lease, so every release that a rollback can reach lets a copy
// take the Lease.
//
// The election links client-go's typed clientset. That costs this
// binary about 13 MB, and it is accepted here because the cluster
// operator runs as one pod in the fleet. The machine operator runs on
// every machine and never imports the package.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/election"
)

// leaseName names the Lease the copies compete for, and leaseNamespace
// is the namespace the whole OS uses. The sweep also reads this Lease
// from its watch, to judge how fresh its copies are (watches.go).
const (
	leaseName      = "liken-cluster-operator"
	leaseNamespace = "liken-system"
)

// electionOptions names this program's Lease and pod for the election.
// report receives each line with this program's prefix.
func electionOptions(pod string, report func(string)) election.Options {
	return election.Options{
		Name:      leaseName,
		Namespace: leaseNamespace,
		Pod:       pod,
		Report:    func(line string) { report(component + ": " + line) },
	}
}

// lead blocks until this process may act. It exits the process when
// stop ends first, and whenever the Lease is lost after that. The pod's
// hostname is its name, which makes the identity readable in `kubectl
// get lease`.
func lead(stop context.Context) *election.Lead {
	pod, err := os.Hostname()
	if err != nil {
		fatal("reading the pod's name for the leader election: %v", err)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fatal("in-cluster config for the leader election: %v", err)
	}
	options := electionOptions(pod, func(line string) { fmt.Println(line) })
	l, err := election.Start(stop, config, options)
	if errors.Is(err, election.ErrStopped) {
		os.Exit(0)
	}
	if err != nil {
		fatal("leader election: %v", err)
	}
	return l
}
