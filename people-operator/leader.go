package main

// Only one copy of the operator may act at a time. When two copies
// handle the same finished baker pod, one reads its log, writes the
// status, and deletes the pod. The other then finds no log and writes
// AvatarReady False with reason BakeFailed, although the pod read the
// picture.
// A rolling update, a network partition, or more than one replica can
// start a second copy, so the copies elect a leader through
// kubernetes/election.
//
// A copy that does not hold the Lease only waits for it. The client
// calls Lead.MayWrite before each write, so a leader that cannot renew
// the Lease stops writing before another copy can take it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/election"
	"github.com/liken-sh/liken/kubernetes/events"
)

// leaseName is the Lease the copies compete for, in the operator's own
// namespace.
const leaseName = "people-operator"

// process is one running copy of the operator.
type process struct {
	// client sends every request of the operator. main builds it with
	// WithWaitContext(stop): when stop ends, a wait to retry after a 429
	// ends, and a request already sent runs to its end. So each write
	// either returns before operate releases the Lease or is never sent.
	client *apiclient.Client
	// watcher opens the watches. It needs no write guard, because a
	// watch only reads.
	watcher dynamic.Interface
	// lease is the configuration the election uses for the Lease.
	lease *rest.Config
	// transport sends the requests to https:// and http:// picture
	// servers.
	transport http.RoundTripper

	// pod and namespace are the name and namespace of the operator's own
	// pod. The election writes the pod name, with a random suffix, as
	// this copy's holder identity in the Lease, and creates the Lease in
	// namespace.
	pod       string
	namespace string

	// report receives the election's log lines, and eventLog receives
	// the Event recorder's. exit ends the process when it loses the
	// Lease. Tests replace all three.
	report   func(line string)
	eventLog io.Writer
	exit     func(code int)
}

// operate waits for the Lease, then runs the operator until stop ends.
// It returns nil when stop ends before this copy gets the Lease.
//
// operate releases the Lease only after run has returned and the Event
// recorder has shut down, so no write of this copy is in flight when
// another copy takes over. run returns when the pass in progress
// finishes the Person it is on. The recorder posts Events from its own
// goroutine, and Shutdown returns when its last request has returned.
// When Shutdown times out, operate returns the error and keeps the
// Lease. Another copy then takes the Lease only when it expires, which
// gives a write still in flight the Lease's duration to finish.
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
	image, err := ownImage(client, p.namespace, p.pod)
	if err != nil {
		// This copy has written nothing, so another copy can take the
		// Lease now.
		lead.StepDown()
		return err
	}
	bakers := &bakers{client: client, image: image, namespace: p.namespace}
	recorder := events.New(context.Background(), client, component, events.Options{Instance: p.pod, Log: p.eventLog})
	newOperator(client, p.transport, bakers, recorder).run(stop, p.watcher)
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		return fmt.Errorf("leaving the Lease to expire because the Event recorder did not shut down: %w", err)
	}
	lead.StepDown()
	return nil
}
