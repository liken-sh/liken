package main

// Only one copy of this program acts on the fleet at a time. A pass
// writes the Lost phase, the Cluster's status, and reboot grants, evicts
// pods, and deletes workloads. Two copies that reconcile at the same time can
// each grant a reboot turn from a different view of the fleet, and
// together exceed the disruption budget. A rolling update, a network
// partition, and a replica count above one each produce a second copy.
// So the copies elect one actor through kubernetes/election, with a
// coordination.k8s.io Lease named liken-cluster-operator in
// liken-system. That package documents the election, its timings, and
// its limits.
//
// The election does not fence a former leader. A leader that paused can
// still send a grant it prepared before the pause, after another copy
// took the Lease. The Cluster's status.rebootTurns keeps the budget
// against such a grant, because a pass records each turn there before
// it grants it (rollout.go).
//
// A copy whose request for the Lease the API server refuses keeps
// waiting, as for any other election failure. The OS image has this
// binary, so after a rollback it runs under the RBAC of the older
// release's manifests. The Cluster CRD refuses a spec.version older than
// 2026.09.28-001, the first release whose RBAC lets this program create
// and update its Lease, so every release that a rollback can reach
// grants that access.
//
// kubernetes/election links client-go's typed clientset, which makes
// this binary larger. That is acceptable for one pod. The machine
// operator runs on every machine and does not import the package.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/election"
)

// leaseName is the Lease the copies compete for, and leaseNamespace is
// the namespace of the OS's own objects. The fleet watches also read
// this Lease (watches.go).
const (
	leaseName      = "liken-cluster-operator"
	leaseNamespace = "liken-system"
)

// electionOptions returns this program's election settings: its Lease,
// the pod's name as its identity, and a Report that prefixes each log
// line with the component name.
func electionOptions(pod string, report func(string)) election.Options {
	return election.Options{
		Name:      leaseName,
		Namespace: leaseNamespace,
		Pod:       pod,
		Report:    func(line string) { report(component + ": " + line) },
	}
}

// lead blocks until this process holds the Lease. It exits the process
// when stop ends first, and later when the Lease is lost. The identity
// is the pod's hostname, which is the pod's name, so `kubectl get lease`
// shows which pod holds it.
func lead(stop context.Context) *election.Lead {
	pod, err := os.Hostname()
	if err != nil {
		fatal("reading the hostname as the pod's name for the leader election: %v", err)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fatal("reading the in-cluster configuration for the leader election: %v", err)
	}
	options := electionOptions(pod, func(line string) { fmt.Println(line) })
	l, err := election.Start(stop, config, options)
	if errors.Is(err, election.ErrStopped) {
		os.Exit(0)
	}
	if err != nil {
		fatal("starting the leader election: %v", err)
	}
	return l
}
