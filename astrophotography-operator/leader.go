package main

// Two operator pods acting at once could claim one Target for two
// reservations, send the same exposure to one camera, or start two
// promotion Jobs that append to one catalog. A rollout, a node
// partition, or more than one replica can run a second pod. So only the
// pod that holds the Lease astrophotography-operator acts, and the
// others wait for it through kubernetes/election. The API client checks
// Lead.MayWrite before each write, so a leader that cannot renew the
// Lease stops writing before another pod can take it.

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

// leaseName is the name of the Lease that the copies compete for, in
// the operator's own namespace. deploy/rbac.yaml grants access to the
// Lease by this name.
const leaseName = managedBy

// process holds the clients and settings of one operator process.
type process struct {
	// client sends every request of the operator except the watches.
	client  *apiclient.Client
	watcher dynamic.Interface
	// lease is the client configuration for the election.
	lease *rest.Config

	// pod and namespace are the operator's own pod name and namespace.
	// The Lease holder identity starts with the pod name.
	pod       string
	namespace string

	// metricsAddress is where /metrics listens. When it is empty, the
	// process serves no metrics.
	metricsAddress string

	// report receives each line that the election logs. logs receives
	// the log lines of the operator and its Event recorder. exit ends
	// the process after the Lease is lost. A test sets all three.
	report func(line string)
	logs   io.Writer
	exit   func(code int)
}

// operate waits for the Lease, runs the operator until stop ends, and
// then releases the Lease. When stop ends before this copy becomes the
// leader, operate returns nil.
//
// No write may be in flight when the Lease is released. o.run returns
// only after every goroutine that writes has returned, and
// recorder.Shutdown returns after the recorder's last request. When
// Shutdown does not finish within events.ShutdownTimeout, operate
// returns the error and does not release the Lease, so a waiting pod
// takes it only when it expires.
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
		return fmt.Errorf("electing a leader: %w", err)
	}
	client := p.client.WithWriteGuard(lead.MayWrite)
	// The recorder uses a context that stop does not cancel, so the
	// Events of the last pass are written before the Lease is released.
	recorder := events.New(context.Background(), client, managedBy, events.Options{Instance: p.pod, Log: p.logs})
	o := newOperator(client, recorder, newMetrics(version), p.logs)
	o.pod, o.namespace = p.pod, p.namespace
	// When workerImage fails, the leader still runs, and each Solver's
	// Ready condition reports that its pod cannot start.
	if o.workerImage, err = workerImage(client, p.namespace, p.pod); err != nil {
		fmt.Fprintf(p.logs, "%s: reading the worker image: %v\n", managedBy, err)
	}
	// Only the leader serves metrics, because only the leader records
	// them. When the listener fails to open, the operator logs it and
	// continues.
	if _, err := o.metrics.serve(stop, p.metricsAddress); err != nil {
		fmt.Fprintf(p.logs, "%s: opening the metrics listener: %v\n", managedBy, err)
	}
	o.run(stop, p.watcher)
	ctx, cancel := context.WithTimeout(context.Background(), events.ShutdownTimeout)
	defer cancel()
	if err := recorder.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting down the Event recorder: %w, so the Lease is left to expire", err)
	}
	lead.StepDown()
	return nil
}
