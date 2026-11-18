package main

// The operator runs in the pod that holds the leader Lease. It
// opens the watches (watch.go) and runs one loop for the Solver pods,
// one for the worker pods, one for the promotion Jobs, and one that
// starts the runner of each Reservation (runners.go). Each loop waits
// until a watch reports a change through o.changed, then reads the
// stores. run returns only after every loop has stopped, so the caller
// releases the Lease after the last write.

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
	// client refuses every write once this replica cannot show that it
	// still holds the Lease (apiclient.WithWriteGuard).
	client   *apiclient.Client
	recorder *events.Recorder
	metrics  *metrics
	logs     io.Writer
	// workerImage is the image of the solver and worker pods
	// (images.go). It is empty when the leader could not find it, and
	// then no solver or worker pod is created.
	workerImage string

	changed *changeNotifier
	stores  *stores

	// pod and namespace name the operator's own pod. The promotion Jobs
	// run that pod's image (ownimage.go).
	pod       string
	namespace string

	// targetVersions records the resourceVersion of each Target that
	// the operator last wrote or read, so a pass does not act on a
	// cached copy older than its own last write (informer.ReadOne).
	targetVersions *memo.Versions
	// deploymentVersions and serviceVersions do the same for each
	// Deployment and Service, so a pass does not create one twice
	// before the watch delivers it (workload.go).
	deploymentVersions, serviceVersions *memo.Versions
	// indiDialer opens each runner's connection to its telescope's INDI
	// server, and peerClient sends requests to the worker and solver pods.
	// Tests replace both with in-memory fakes.
	indiDialer indi.Dialer
	peerClient *http.Client
	// phd2Dialer opens each runner's connection to PHD2's event server
	// (guider.go). Tests replace it with an in-memory fake PHD2.
	phd2Dialer phd2.Dialer
	// centering and guiding are the steps that call the plate solver
	// and PHD2 (steps.go). Tests replace them to record the order of the
	// steps.
	centering centering
	guiding   guiding
}

func newOperator(client *apiclient.Client, recorder *events.Recorder, m *metrics, logs io.Writer) *operator {
	return &operator{
		client: client, recorder: recorder, metrics: m, logs: logs, changed: newChangeNotifier(),
		targetVersions:     memo.New(),
		deploymentVersions: memo.New(),
		serviceVersions:    memo.New(),
		indiDialer:         &net.Dialer{Timeout: indi.DialTimeout},
		peerClient:         &http.Client{},
		centering:          plateCentering{},
		guiding:            newPHD2Guiding(),
		phd2Dialer:         &net.Dialer{Timeout: phd2.DialTimeout},
	}
}

// run opens the watches and starts the loops. It returns after ctx ends
// and every loop, runner, and watch has stopped.
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
	solvers.wait()
	o.stores.done()
}

// retryAfter is how long a loop waits to run a failed pass again, when
// no change wakes it sooner. It is a retry backoff, not a poll.
const retryAfter = 10 * time.Second

// loop runs pass on each change, once every watch has listed its
// collection, until ctx ends.
func (o *operator) loop(ctx context.Context, kind string, pass func(context.Context) error) {
	for {
		notified := o.changed.wait()
		var retry <-chan time.Time
		if o.stores.synced() {
			started := time.Now()
			err := pass(ctx)
			o.metrics.observePass(kind, time.Since(started), err != nil)
			if err != nil {
				fmt.Fprintf(o.logs, "%s: the %s loop: %v\n", managedBy, kind, err)
				retry = time.After(retryAfter)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-notified:
		case <-retry:
		}
	}
}
