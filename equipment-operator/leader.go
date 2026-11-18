//go:build !node

package main

// Only one copy of the operator may act at a time. Two copies would both
// open a session on the same receiver and send it commands, or both
// create the same Television. The Deployment has one replica and the
// Recreate strategy, but a node partition or a higher replica count can
// still start a second copy. So the copies elect a leader through a
// Lease, with kubernetes/election, and only the leader does any work.
//
// The node build (build tag node) leaves this file out, and
// leader_node.go gives the reason.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/election"
)

// leaseName is the name of the Lease, in the operator's own namespace.
const leaseName = "equipment-operator"

// leadership is this process's place in the election, with a client
// that writes only while this process leads.
type leadership struct {
	*election.Lead

	// client refuses every write once this process has gone
	// election.RenewDeadline without renewing the Lease, which is before
	// another copy can take it. The guard is in the client, so it covers
	// the writes of every loop.
	client *Client
}

func newLeadership(client *Client, config *rest.Config, options election.Options) (*leadership, error) {
	lead, err := election.New(config, options)
	if err != nil {
		return nil, err
	}
	guarded := &Client{Client: client.Client.WithWriteGuard(lead.MayWrite), access: client.access}
	return &leadership{Lead: lead, client: guarded}, nil
}

// actWhileLeading waits until this process holds the Lease, runs act
// with the guarded client, and then releases the Lease. act is the whole
// operator, so a copy that waits for the Lease does nothing else. led
// reports whether this process took the Lease before stop ended.
//
// When act returns errStillWriting, a write may still be in flight, so
// the process exits without releasing the Lease, and another copy takes
// it only when it expires.
func (l *leadership) actWhileLeading(stop context.Context, act func(*Client) error) (led bool, err error) {
	if !l.Await(stop) {
		return false, nil
	}
	err = act(l.client)
	if errors.Is(err, errStillWriting) {
		return true, err
	}
	l.StepDown()
	return true, err
}

// leaderRequestTimeout makes the client give up on a write that the
// guard allowed before another copy can take the Lease. It equals
// election.RequestTimeout, whose comment gives the timeline.
const leaderRequestTimeout = election.Duration - election.RenewDeadline - election.RetryPeriod

// lead creates the operator's client and starts the election. It exits
// when the pod's name or namespace is unset, because the namespace holds
// the Lease and the pod's name is this copy's identity in the election.
func lead(config settings) *leadership {
	if config.namespace == "" || config.pod == "" {
		fmt.Fprintf(os.Stderr, "%s and %s are unset, so the operator has no namespace for its Lease and no holder identity\n",
			podNamespaceVariable, podNameVariable)
		os.Exit(1)
	}
	client, err := InClusterClient(leaderRequestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the in-cluster configuration: %v\n", err)
		os.Exit(1)
	}
	l, err := newLeadership(client, client.restConfig(), election.Options{
		Name:      leaseName,
		Namespace: config.namespace,
		Pod:       config.pod,
		Report:    func(line string) { fmt.Fprintln(os.Stderr, line) },
		Exit:      os.Exit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting the leader election: %v\n", err)
		os.Exit(1)
	}
	l.Run()
	return l
}
