package main

// The process watches every namespace and runs one operator for each
// namespace that has an object of the group or an object that an
// operator created. Two namespaces can use the same resource names,
// and each operator reads only its own namespace, so a dome lock or a
// mount lock never applies across namespaces.
//
// Every pod, Service, ConfigMap, claim, and Job of an observatory is
// created in the namespace of its resources. So the ResourceQuota,
// NetworkPolicy, Pod Security level, and RBAC that the cluster owner
// sets on that namespace apply to the observatory.

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/indi"
)

// namespaces runs the operator of each namespace.
type namespaces struct {
	client   *apiclient.Client
	dialer   indi.Dialer
	logs     io.Writer
	recorder *events.Recorder
	// metrics is the registry that the listener serves (metrics.go).
	metrics *metrics
	// changed is notified on each change to the stores.
	changed *changeNotifier
	stores  *stores

	mu      sync.Mutex
	running map[string]*namespaceRun
}

// namespaceRun is the operator of one namespace while it runs.
type namespaceRun struct {
	o    *operator
	stop context.CancelFunc
	done chan struct{}
}

func newNamespaces(client *apiclient.Client, dialer indi.Dialer) *namespaces {
	return &namespaces{
		client:  client,
		dialer:  dialer,
		logs:    os.Stderr,
		metrics: newMetrics(version),
		changed: newChangeNotifier(nil),
		running: map[string]*namespaceRun{},
	}
}

// run opens the watches and runs one operator for each namespace until
// ctx ends.
func (n *namespaces) run(ctx context.Context, watches func(context.Context, *changeNotifier) *stores) {
	n.stores = watches(ctx, n.changed)
	var group sync.WaitGroup
	defer func() {
		n.mu.Lock()
		for _, r := range n.running {
			r.stop()
		}
		n.mu.Unlock()
		group.Wait()
		n.stores.done()
	}()
	for {
		wake := n.changed.wait()
		if n.stores.ready() {
			n.supervise(ctx, &group)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// supervise starts an operator for each namespace that has objects and
// stops the operator of each namespace that has none. It notifies every
// running operator, because the stores are shared and the change may be
// in any namespace.
func (n *namespaces) supervise(ctx context.Context, group *sync.WaitGroup) {
	held := n.stores.namespaces()
	n.mu.Lock()
	defer n.mu.Unlock()
	for namespace, r := range n.running {
		if !held[namespace] {
			r.stop()
		}
	}
	for namespace := range held {
		if _, running := n.running[namespace]; !running {
			n.start(ctx, group, namespace)
		}
	}
	for _, r := range n.running {
		r.o.structure.notify()
	}
}

// start runs the operator of one namespace. The caller holds n.mu. A
// stopped operator stays in running until its run returns, so a
// namespace never has two operators at once.
func (n *namespaces) start(ctx context.Context, group *sync.WaitGroup, namespace string) {
	o := newOperator(namespace, n.client, n.dialer)
	o.logs, o.recorder, o.series = n.logs, n.recorder, n.metrics.namespace(namespace)
	ctx, stop := context.WithCancel(ctx)
	r := &namespaceRun{o: o, stop: stop, done: make(chan struct{})}
	n.running[namespace] = r
	group.Go(func() {
		defer close(r.done)
		o.run(ctx, n.stores)
		n.mu.Lock()
		delete(n.running, namespace)
		n.mu.Unlock()
		// The namespace may have objects again, so the next supervise
		// may start a new operator for it.
		n.changed.notify()
	})
}

// operatorOf returns the running operator of a namespace, or nil when
// the namespace has none.
func (n *namespaces) operatorOf(namespace string) *operator {
	n.mu.Lock()
	defer n.mu.Unlock()
	if r, ok := n.running[namespace]; ok {
		return r.o
	}
	return nil
}
