package main

// The operator of the copy that holds the Lease. It opens the watches
// (watch.go), and every loop it runs waits on the bell changed and reads
// the stores. Plan 01 adds the runner of each Reservation it holds,
// plan 02 the Solver's and the worker's pods, and plan 03 the promotion
// Jobs. Each starts in run and returns before run does, so the caller
// releases the Lease only after the last write.

import (
	"context"
	"io"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
)

type operator struct {
	// client sends every request, through the lead's write guard.
	client *apiclient.Client
	// recorder posts the Events (events.go).
	recorder *events.Recorder
	metrics  *metrics
	// logs receives the operator's log: standard error in a pod, and the
	// test's log in a test.
	logs io.Writer

	changed *bell
	stores  *stores
}

func newOperator(client *apiclient.Client, recorder *events.Recorder, m *metrics, logs io.Writer) *operator {
	return &operator{client: client, recorder: recorder, metrics: m, logs: logs, changed: newBell()}
}

// run opens the watches and runs until ctx ends. It returns once every
// watch has stopped.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	o.stores = startWatches(ctx, watcher, o.changed, o.metrics)
	<-ctx.Done()
	o.stores.done()
}
