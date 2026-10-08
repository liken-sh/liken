package main

// The namespaces. The process watches every namespace and runs one
// operator for each namespace that holds an object of the group or an
// object the operator created. An observatory references its parts by
// name, and the names of two namespaces can be the same, so each
// namespace gets its own runners, connections, locks, and records. A
// dome lock or a mount lock never crosses a namespace, because no
// operator reads another namespace's tree.
//
// The cluster owner chooses the namespace, and every pod, Service,
// ConfigMap, claim, and Job of an observatory runs in the namespace of
// its resources. So the owner's ResourceQuota, NetworkPolicy, Pod
// Security level, and RBAC grants in that namespace cover the
// observatory.

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
	// changed rings on each change to the stores.
	changed *bell
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
		changed: newBell(nil),
		running: map[string]*namespaceRun{},
	}
}

// run opens the watches and runs the operator of each namespace until
// ctx ends.
func (n *namespaces) run(ctx context.Context, watches func(context.Context, *bell) *stores) {
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

// supervise starts the operator of each namespace that holds objects,
// stops the operator of each namespace that holds none, and wakes each
// operator that runs, since a change to the stores is a change to its
// tree or to another namespace's.
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

// start runs the operator of one namespace. The caller holds n.mu. The
// operator leaves running when it stops, so a namespace that holds
// objects again after a stop gets a new operator, and never two at
// once.
func (n *namespaces) start(ctx context.Context, group *sync.WaitGroup, namespace string) {
	o := newOperator(namespace, n.client, n.dialer)
	o.logs, o.recorder = n.logs, n.recorder
	ctx, stop := context.WithCancel(ctx)
	r := &namespaceRun{o: o, stop: stop, done: make(chan struct{})}
	n.running[namespace] = r
	group.Go(func() {
		defer close(r.done)
		o.run(ctx, n.stores)
		n.mu.Lock()
		delete(n.running, namespace)
		n.mu.Unlock()
		// The next pass starts the namespace again if it holds objects
		// again.
		n.changed.notify()
	})
}

// operatorOf answers the operator of one namespace, or nil when none
// runs.
func (n *namespaces) operatorOf(namespace string) *operator {
	n.mu.Lock()
	defer n.mu.Unlock()
	if r, ok := n.running[namespace]; ok {
		return r.o
	}
	return nil
}
