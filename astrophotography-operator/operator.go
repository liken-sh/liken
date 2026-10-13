package main

// The operator of the copy that holds the Lease. It opens the watches
// (watch.go), and every loop it runs waits on the bell changed and reads
// the stores. Plan 01 adds the runner of each Reservation it holds
// (runners.go), plan 02 the Solver's and the worker's pods, and plan 03
// the promotion Jobs. Each starts in run and returns before run does,
// so the caller releases the Lease only after the last write.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
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
	// workerImage is the image of the solvers' and the workers' pods
	// (images.go), and empty when the lead could not name it.
	workerImage string

	changed *bell
	stores  *stores

	// pod and namespace name the operator's own pod, whose image the
	// promotion Jobs run (ownimage.go).
	pod       string
	namespace string

	// targetVersions records the version of each Target the operator
	// wrote or read, so a pass never acts on a store's copy that is
	// older than its own last write (informer.ReadOne).
	targetVersions *memo.Versions
	// indiDialer opens each runner's connection to its telescope's INDI
	// server, and workerClient sends each request to a reservation's
	// worker. A test replaces both with fakes in memory.
	indiDialer   indi.Dialer
	workerClient *http.Client
	// phd2Dialer opens each runner's connection to PHD2's event server
	// (guider.go). A test replaces it with a fake PHD2 in memory.
	phd2Dialer phd2.Dialer
	// centering and guiding are the steps of plan 01's order that
	// pieces H and I of plan 08 fill in (steps.go). Every runner takes
	// them from here, and a test replaces them to watch the order.
	centering centering
	guiding   guiding
}

func newOperator(client *apiclient.Client, recorder *events.Recorder, m *metrics, logs io.Writer) *operator {
	return &operator{
		client: client, recorder: recorder, metrics: m, logs: logs, changed: newBell(),
		targetVersions: memo.New(),
		indiDialer:     &net.Dialer{Timeout: indi.DialTimeout},
		workerClient:   &http.Client{},
		centering:      plateCentering{},
		guiding:        newPHD2Guiding(),
		phd2Dialer:     &net.Dialer{Timeout: phd2.DialTimeout},
	}
}

// run opens the watches and runs the runners until ctx ends. It returns
// once every runner and every watch has stopped.
func (o *operator) run(ctx context.Context, watcher dynamic.Interface) {
	o.stores = startWatches(ctx, watcher, o.changed, o.metrics)
	var loops sync.WaitGroup
	solvers, workers := newSolverLoop(), newWorkerLoop()
	loops.Go(func() {
		o.loop(ctx, astrophotography.SolverKind.Name, func(ctx context.Context) error { return o.solverPass(ctx, solvers) })
	})
	loops.Go(func() {
		o.loop(ctx, observatory.ReservationKind.Name, func(ctx context.Context) error { return o.workerPass(ctx, workers) })
	})
	loops.Go(func() { o.promote(ctx) })
	loops.Go(func() { o.superviseRunners(ctx) })
	<-ctx.Done()
	loops.Wait()
	o.stores.done()
}

// retryAfter is the wait before a pass that failed runs again with no
// change to wake it: a write the API server refused, or a solver's pod
// that did not answer its index list. It is a backoff, a clock, and
// not a poll: a change wakes the pass sooner.
const retryAfter = 10 * time.Second

// loop runs a pass each time the bell rings, once every watch has read
// its collection, until ctx ends. A pass that fails runs again after
// retryAfter.
func (o *operator) loop(ctx context.Context, kind string, pass func(context.Context) error) {
	for {
		rang := o.changed.wait()
		var retry <-chan time.Time
		if o.stores.synced() {
			started := time.Now()
			err := pass(ctx)
			o.metrics.observePass(kind, time.Since(started), err != nil)
			if err != nil {
				fmt.Fprintf(o.logs, "%s: a pass of the %s loop: %v\n", managedBy, kind, err)
				retry = time.After(retryAfter)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-rang:
		case <-retry:
		}
	}
}
