package main

// The operator is a cluster singleton. It keeps the holder of each
// telescope in memory (claims.go), runs one runner for each
// Reservation, and sends commands to each device through the INDI
// servers. Two copies that act at once each read the same holders
// from the status, so both run the same Reservation's steps: both
// create the device pods, one deletes a pod that the other just made,
// both unpark the mount and open the dome, and their status writes
// overwrite each other. A rollout, a node partition, and a replica
// count above one each run a second copy, so only the copy that holds
// the Lease of kubernetes/election acts. Every other copy reads the
// Lease and does nothing else.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// leaseName names the Lease that the operator's copies compete for, in
// the operator's own namespace. deploy/rbac.yaml grants it by this
// name.
const leaseName = "observatory-operator"

// actWhileLeading waits for the Lease, runs act, and releases the Lease
// after act returns. act is everything the operator does: it opens the
// watches, starts the runners, opens the INDI and PHD2 connections, and
// writes the API. So a copy that waits does none of that. act receives
// the lead's write guard for its client.
//
// act returns on a shutdown only after every goroutine that writes has
// returned (namespaces.run) and the Event recorder's last write has
// returned (namespaces.finish), so the release comes after the last
// write to the API and the last command to a device. When act answers
// an error, a write can still be in flight, so the Lease is not
// released: the process exits holding it, and a waiting copy takes it
// when it expires. A copy that stop ends before it leads holds nothing
// and returns nil.
func actWhileLeading(stop context.Context, config *rest.Config, options election.Options, act func(mayWrite func() error) error) error {
	lead, err := election.Start(stop, config, options)
	if errors.Is(err, election.ErrStopped) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("leader election: %w", err)
	}
	if err := act(lead.MayWrite); err != nil {
		return fmt.Errorf("%w; leaving the Lease to expire", err)
	}
	lead.StepDown()
	return nil
}

// leadingNamespaces builds the operator of every namespace on a client
// that mayWrite guards. The Events recorder writes through the same
// client, so the guard covers every write the process sends: a copy
// whose last renewal of the Lease is too old sends nothing, and after
// the release nothing writes. The recorder outlives the stop, so the
// Events of the last pass are written, and finish shuts it down before
// the release.
func leadingNamespaces(client *apiclient.Client, mayWrite func() error,
	dialer indi.Dialer, logs io.Writer, instance string) *namespaces {
	guarded := client.WithWriteGuard(mayWrite)
	n := newNamespaces(guarded, dialer)
	if logs != nil {
		n.logs = logs
	}
	n.recorder = events.New(context.Background(), guarded, managedBy, events.Options{Instance: instance, Log: n.logs})
	return n
}

// finish shuts the Event recorder down after run has returned, and
// answers an error when the recorder's last write did not return in
// time.
func (n *namespaces) finish() error {
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	return n.recorder.Shutdown(ctx)
}
