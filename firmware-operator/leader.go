package main

// The operator is a cluster singleton, because two copies that act at
// once race on the upload pods. Both copies follow the same finished
// pod. One copy writes its result and deletes the pod. The other copy
// then finds a Running entry with no pod, and writes Failed over an
// upload that loaded. Two copies also post every Event twice. A rolling
// update, a node partition, and a replica count above one each run a
// second copy, so the operator elects one acting copy with the Lease
// named firmware-operator, through kubernetes/election.
//
// Only the copy that holds the Lease watches, writes, and starts upload
// pods. Every other copy waits for the Lease and does nothing else. The
// client asks the election before each write (Lead.MayWrite), so a
// leader that cannot renew stops writing before another copy can take
// the Lease.

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

// leaseName names the Lease the copies compete for, in the operator's
// own namespace.
const leaseName = "firmware-operator"

// process is what one process of the operator reaches: the API server
// and the Lease.
type process struct {
	// client sends every request of the operator. It ends the wait after
	// a 429 on the stop, and does not cut off a request in flight, so a
	// write either returns before the release or was never sent.
	client *apiclient.Client
	// watcher is the client of the watches, which only read.
	watcher dynamic.Interface
	// lease is the configuration of the election's client.
	lease *rest.Config

	// pod and namespace name the operator's own pod. The pod's name is
	// the start of the identity the Lease names, and the namespace holds
	// the Lease and the upload pods.
	pod       string
	namespace string

	// discovery holds the flags of discovery.
	discovery discoverySettings

	// report receives each line the election logs, and eventLog each
	// line of the Event recorder. exit ends the process after a lost
	// Lease. A test sets all three.
	report   func(line string)
	eventLog io.Writer
	exit     func(code int)
}

// operate waits for the Lease, and then runs the operator until stop
// ends. A copy that never led answers nil when stop ends, because it
// holds nothing.
//
// The release comes after run returns and the Event recorder has shut
// down. run returns only after the pass in flight has answered its
// rule, and every write of that pass returns before the next request is
// sent. The recorder writes the Events of the last pass from its own
// goroutine, and Shutdown returns after its last request has returned.
// So no write is in flight when the Lease is free. When the recorder's
// last write does not return in time, operate answers the error and
// keeps the Lease: the process exits holding it, and a waiting copy
// takes it when it expires.
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
		return fmt.Errorf("%w; leaving the Lease to expire", err)
	}
	lead.StepDown()
	return nil
}
