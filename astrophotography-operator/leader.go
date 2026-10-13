package main

// The operator is a cluster singleton. Two copies that act at once
// would both claim the same Target for a reservation, both send the
// same exposure to one camera, and both start a promotion Job that
// appends to one catalog, which must have one writer. A rollout, a
// node partition, and a replica count above one each run a second copy,
// so only the copy that holds the Lease astrophotography-operator acts,
// through kubernetes/election. Every other copy waits for the Lease and
// does nothing else. The client asks the election before each write
// (Lead.MayWrite), so a leader that cannot renew stops writing before
// another copy can take the Lease.

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

// leaseName names the Lease that the copies compete for, in the
// operator's own namespace. deploy/rbac.yaml grants it by this name.
const leaseName = managedBy

// process is what one process of the operator reaches: the API server
// and the Lease.
type process struct {
	// client sends every request of the operator.
	client *apiclient.Client
	// watcher is the client of the watches, which only read.
	watcher dynamic.Interface
	// lease is the configuration of the election's client.
	lease *rest.Config

	// pod and namespace name the operator's own pod. The pod's name is
	// the start of the identity the Lease names, and the namespace holds
	// the Lease.
	pod       string
	namespace string

	// metricsAddress is where /metrics listens. Empty turns it off.
	metricsAddress string

	// report receives each line the election logs, and logs each line
	// of the operator and its Event recorder. exit ends the process
	// after a lost Lease. A test sets all three.
	report func(line string)
	logs   io.Writer
	exit   func(code int)
}

// operate waits for the Lease, then runs the operator until stop ends,
// and releases the Lease after the last write. A copy that stop ends
// before it leads holds nothing and answers nil.
//
// run returns only after every goroutine that writes has returned. The
// Event recorder writes from its own goroutine, and Shutdown returns
// after its last request has returned. So no write is in flight when
// the Lease is free. When the recorder's last write does not return in
// time, operate answers the error and keeps the Lease: the process
// exits holding it, and a waiting copy takes it when it expires.
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
		return fmt.Errorf("leader election: %w", err)
	}
	client := p.client.WithWriteGuard(lead.MayWrite)
	// The recorder outlives the stop, so the Events of the last pass
	// are written before the release.
	recorder := events.New(context.Background(), client, managedBy, events.Options{Instance: p.pod, Log: p.logs})
	o := newOperator(client, recorder, newMetrics(version), p.logs)
	o.pod, o.namespace = p.pod, p.namespace
	// Only the copy that holds the Lease serves metrics, because only
	// that copy has series to serve. A listener that fails to open is
	// logged and does not stop the operator's work.
	if _, err := o.metrics.serve(stop, p.metricsAddress); err != nil {
		fmt.Fprintf(p.logs, "%s: the metrics listener: %v\n", managedBy, err)
	}
	o.run(stop, p.watcher)
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		return fmt.Errorf("%w; leaving the Lease to expire", err)
	}
	lead.StepDown()
	return nil
}
