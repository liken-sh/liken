//go:build !pod

package main

// The operator is a cluster singleton. It creates and deletes Jobs,
// pods, and claims, and writes the status of every Library and Catalog,
// so two copies that reconcile at the same time race: both create a Job
// for one scan, or one deletes the screen pod the other just made. A
// rollout, a node partition, and a replica count above one each run a
// second copy, so the operator enforces one acting copy in its own code,
// with leader election on a coordination.k8s.io Lease.
//
// The election is the shared kubernetes/election package. Only the
// copy that holds the Lease opens a bus session, watches, and
// reconciles, and every write it sends asks the election first
// (election.Lead.MayWrite). Every other copy waits, so a rollout starts
// the new pod beside the old one, and the new pod takes over when the
// old one releases the Lease. The package gives the timings and the
// reasons for them.

import (
	"context"

	"github.com/liken-sh/liken/kubernetes/election"
	"k8s.io/client-go/rest"
)

// leaseName names the Lease the operator's copies compete for, in the
// operator's own namespace.
const leaseName = "library-operator"

// leadership is this process's part in the election, with the order of
// a shutdown that the bus session needs (stepDown).
type leadership struct {
	*election.Lead
	report func(line string)
}

// newLeadership builds the election. The identity is the pod's name and
// a random suffix, so a restarted container is a new candidate. exit
// ends the process after a lost Lease, and report receives each line
// the election logs, with the operator's prefix.
func newLeadership(config *rest.Config, namespace, pod string,
	exit func(int), report func(string)) (*leadership, error) {
	prefixed := func(line string) { report("library.liken.sh: " + line) }
	lead, err := election.New(config, election.Options{
		Name:      leaseName,
		Namespace: namespace,
		Pod:       pod,
		Report:    prefixed,
		Exit:      exit,
	})
	if err != nil {
		return nil, err
	}
	return &leadership{Lead: lead, report: prefixed}, nil
}

// await returns true when this process holds the Lease, and false when
// stop ends first. After a false return the election has ended and
// this process holds nothing.
func (l *leadership) await(stop context.Context) bool {
	return l.Await(stop)
}

// stepDown ends this process's lead on a shutdown. The caller calls it
// after the last pass. It runs quiet first, which stops the bus
// session, so the next leader's session under the same client id does
// not meet this one. Then it releases the Lease. quiet answers false
// when the session did not stop in time, and then the Lease is not
// released: the process exits holding it, and a waiting copy takes it
// when it expires.
func (l *leadership) stepDown(quiet func() bool) {
	if !quiet() {
		l.report("the bus session did not stop; leaving the Lease to expire")
		return
	}
	l.StepDown()
}
