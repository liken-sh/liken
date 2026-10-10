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
// Only the copy that holds the Lease opens its watches and sweeps, with
// one exception: a copy that the API server refuses on the Lease acts
// without an election. The OS images carry this binary, so a rollback
// can run it under the RBAC of a release from before the election,
// which grants no write of the Lease. A copy that waited for the Lease
// there would grant no reboot, and the rest of the rollback needs its
// grants. So this program alone turns on election.Options.ActWhenRefused,
// and checks before each sweep that it may still act, because a copy
// in that mode stops acting when it finds another holder.
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
// setUnelected receives the mode each time it changes, for the gauge
// (newUnelectedGauge). report receives each line with this program's
// prefix.
func electionOptions(pod string, setUnelected func(bool), report func(string)) election.Options {
	return election.Options{
		Name:           leaseName,
		Namespace:      leaseNamespace,
		Pod:            pod,
		Report:         func(line string) { report(component + ": " + line) },
		ActWhenRefused: true,
		Unelected:      setUnelected,
	}
}

// lead blocks until this process may act. It exits the process when
// stop ends first, and whenever the Lease is lost after that. The pod's
// hostname is its name, which makes the identity readable in `kubectl
// get lease`.
func lead(stop context.Context, setUnelected func(bool)) *election.Lead {
	pod, err := os.Hostname()
	if err != nil {
		fatal("reading the pod's name for the leader election: %v", err)
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		fatal("in-cluster config for the leader election: %v", err)
	}
	options := electionOptions(pod, setUnelected, func(line string) { fmt.Println(line) })
	l, err := election.Start(stop, config, options)
	if errors.Is(err, election.ErrStopped) {
		os.Exit(0)
	}
	if err != nil {
		fatal("leader election: %v", err)
	}
	return l
}
