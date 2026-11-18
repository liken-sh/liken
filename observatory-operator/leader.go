package main

// Only one copy of the operator may act at a time. Two copies that act
// at once both run the same Reservation's steps: both create the device
// pods, one deletes a pod that the other just created, both unpark the
// mount and open the dome, and their status writes overwrite each
// other. A rollout, a node partition, or more than one replica can run
// a second copy, so only the copy that holds the Lease acts, and the
// others wait for it (kubernetes/election). claims.go relies on this
// when it keeps the holder of each Telescope in memory.

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

// leaseName is the name of the Lease that the operator's copies compete
// for, in the operator's own namespace. deploy/rbac.yaml grants access
// to the Lease by this name.
const leaseName = "observatory-operator"

// actWhileLeading waits to acquire the Lease, runs act, and releases
// the Lease after act returns. act is everything the operator does,
// from opening the watches to sending INDI commands, so a waiting copy
// does none of it. act must guard its API client with the mayWrite
// function it receives.
//
// On a shutdown, act returns only after every goroutine that writes has
// returned (namespaces.run) and the Event recorder's last write has
// finished (namespaces.finish), so the Lease is released after the last
// write and the last command to a device. When act returns an error, a
// write can still be in triggerRun, so the Lease is not released, and a
// waiting copy acquires it when it expires. When stop is cancelled
// before the copy leads, actWhileLeading returns nil without running
// act.
func actWhileLeading(stop context.Context, config *rest.Config, options election.Options, act func(mayWrite func() error) error) error {
	lead, err := election.Start(stop, config, options)
	if errors.Is(err, election.ErrStopped) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("electing a leader: %w", err)
	}
	if err := act(lead.MayWrite); err != nil {
		return fmt.Errorf("%w, so the operator leaves the Lease to expire", err)
	}
	lead.StepDown()
	return nil
}

// leadingNamespaces builds the operator of every namespace on a client
// guarded by mayWrite. The Event recorder writes through the same
// client, so a copy whose last renewal of the Lease is too old writes
// nothing at all. The recorder runs on its own context, so a stop does
// not cancel it and it still writes the Events of the last pass. finish
// shuts it down before the Lease is released.
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

// finish shuts the Event recorder down after run has returned. It
// returns an error when the recorder's last write did not finish within
// events.ShutdownTimeout.
func (n *namespaces) finish() error {
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	return n.recorder.Shutdown(ctx)
}
