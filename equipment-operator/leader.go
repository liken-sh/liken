//go:build !node

package main

// The Deployment's operator is a cluster singleton. It holds one
// session on each receiver, creates and deletes the Receivers and
// Televisions discovery owns, and writes their status, so two copies
// that act at once race: both open a session on one receiver and send
// it commands, or both create one Television. The Deployment runs one
// replica with the Recreate strategy, and a node partition or a
// replica count above one can still run a second copy. So the operator
// keeps to one acting copy with the election of kubernetes/election,
// on the coordination.k8s.io Lease equipment-operator.
//
// Only the copy that holds the Lease binds its metrics port, searches
// for devices, opens a session on a receiver, watches the API server,
// or writes to it. Every other copy sends the API server only its
// reads of the Lease, and the create of the Lease when none exists.
//
// This file links client-go's typed clientset, which the election
// needs and which more than doubles the program. The node workload's
// build, with the build tag node, leaves this file out, and
// leader_node.go says why.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/election"
)

// leaseName names the Lease the operator's copies compete for, in the
// operator's own namespace.
const leaseName = "equipment-operator"

// leadership is this process's part in the election, with the client
// that writes only while this process leads.
type leadership struct {
	*election.Lead

	// client is the operator's client with the election's write guard.
	// A leader that cannot renew its Lease stops writing one renewal
	// deadline after its last renewal, before a waiting copy can take
	// the Lease, and the guard sits in the client, so it covers every
	// write of every loop.
	client *Client
}

// newLeadership builds the election from options, on the Lease that
// config reaches, and guards client's writes with it.
func newLeadership(client *Client, config *rest.Config, options election.Options) (*leadership, error) {
	lead, err := election.New(config, options)
	if err != nil {
		return nil, err
	}
	guarded := &Client{Client: client.Client.WithWriteGuard(lead.MayWrite), access: client.access}
	return &leadership{Lead: lead, client: guarded}, nil
}

// actWhileLeading waits for the Lease, runs act with the guarded
// client, and releases the Lease after act returns. act is everything
// the operator does: it binds the metrics port, searches for devices,
// opens the receiver sessions, and watches and writes the API. So a
// copy that waits does none of that, and only reads the Lease. act
// returns on a shutdown after every part of it that writes has
// stopped, so the release comes after the last write. When act answers
// errStillWriting, a write can still be in flight, so the Lease is not
// released: the process exits holding it, and a waiting copy takes it
// when it expires. led says whether this process took the Lease before
// stop ended.
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

// leaderRequestTimeout bounds each request of the operator's client.
// The write guard refuses a write once the last renewal at T is one
// renewal deadline old, at T+10s, so a write it allowed was sent before
// T+10s. This timeout abandons that write by T+25s, before a waiting
// copy can take the Lease at T+30s. The shared client's default of 30
// seconds would let the write run past T+30s.
const leaderRequestTimeout = election.Duration - election.RenewDeadline - election.RetryPeriod

// lead builds the operator's client and starts the election for this
// pod. It exits the process when the pod does not state its name and
// namespace, which name the Lease and the identity it is held under.
func lead(config settings) *leadership {
	if config.namespace == "" || config.pod == "" {
		fmt.Fprintf(os.Stderr, "%s and %s are unset, so the operator cannot name its Lease\n",
			podNamespaceVariable, podNameVariable)
		os.Exit(1)
	}
	client, err := InClusterClient(leaderRequestTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "leader election: %v\n", err)
		os.Exit(1)
	}
	l.Run()
	return l
}
