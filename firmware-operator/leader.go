package main

// Only one copy of the operator may act at a time, because two copies
// race on the upload pods. Both copies read the same finished pod. One
// writes its result and deletes the pod. The other then finds a Running
// entry with no pod and writes Failed over an upload that loaded. Two
// copies also post every Event twice. A rolling update, a node
// partition, or a replica count above one can each run a second copy,
// so the copies elect a leader through kubernetes/election, using the
// Lease named firmware-operator.
//
// Only the copy that holds the Lease watches, writes, and starts upload
// pods. Every other copy waits for the Lease. The client checks the
// election before each write (Lead.MayWrite), so a leader that cannot
// renew the Lease stops writing before another copy can take it.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
)

const leaseName = "firmware-operator"

// process has what one operator process needs to reach the API server
// and the Lease.
type process struct {
	// client sends every request of the operator. When stop is
	// cancelled, the client stops waiting to retry after a 429, but it
	// does not cancel a request it has sent. So each write either
	// completes before the Lease is released or is never sent.
	client  *apiclient.Client
	watcher dynamic.Interface
	// lease is the client configuration of the election.
	lease *rest.Config

	// pod and namespace are the operator's own pod name and namespace.
	// The copy's identity as Lease holder starts with the pod name, and
	// the namespace holds the Lease and the upload pods.
	pod       string
	namespace string

	discovery discoverySettings

	// report receives each line the election logs, and eventLog each
	// line the Event recorder logs. exit ends the process after the
	// Lease is lost. Tests replace each of them.
	report   func(line string)
	eventLog io.Writer
	exit     func(code int)
}

// operate waits for the Lease, then runs the operator until stop is
// cancelled. A copy that never became leader returns nil.
//
// operate releases the Lease only after run has returned and the Event
// recorder has sent its last Event from its own goroutine, so no write
// of this copy is in flight when another copy can take the Lease. When
// the recorder does not finish within events.ShutdownTimeout, operate
// returns the error and keeps the Lease, and a waiting copy takes the
// Lease when it expires.
func (p process) operate(stop context.Context) error {
	lead, err := election.Start(stop, p.lease, election.Options{
		Name:      leaseName,
		Namespace: p.namespace,
		Pod:       p.pod,
		Report:    p.report,
		Exit:      p.exit,
	})
	if errors.Is(err, election.ErrStopped) {
		return nil
	}
	if err != nil {
		return err
	}
	client := p.client.WithWriteGuard(lead.MayWrite)
	recorder := events.New(context.Background(), client, component, events.Options{Instance: p.pod, Log: p.eventLog})
	newOperator(client, p.namespace, recorder, p.discovery).run(stop, p.watcher)
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down the Event recorder, leaving the Lease to expire: %w", err)
	}
	lead.StepDown()
	return nil
}
