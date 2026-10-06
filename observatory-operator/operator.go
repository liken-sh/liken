package main

// The operator runs three kinds of goroutine on the stores of the
// watches (watch.go):
//
//   - The supervisor below starts a runner for each Reservation that
//     has work left, opens an INDI connection to each server pod
//     (indiconn.go), and deletes the pods of a server that no
//     reservation holds.
//   - One runner for each Reservation runs its steps in order
//     (reservation.go).
//   - The status writer writes the status of every other resource
//     (status.go).
//
// Each of them waits on one bell that every watch event and every
// INDI event notifies.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type operator struct {
	namespace string
	client    *apiclient.Client
	stores    *stores
	changed   *bell
	// dialer opens each INDI connection. Nil dials the network.
	dialer indi.Dialer

	servers *servers
	claims  *claims
	// versions records the version of each reservation that the
	// operator wrote or read last, so a runner never acts on an older
	// copy from a store (informer.ReadOne).
	versions *memo.Versions

	mu      sync.Mutex
	runners map[string]*runner
	// faults holds the last failure of each device by its key, such as
	// Camera/east-main, for the device's status.
	faults map[string]string
	// sites serializes the work on each observatory's server, which
	// the runners of several telescopes share.
	sites map[string]lock

	// snapshotMu guards last, the tree that snapshot built since the
	// bell last rang, and lastBell, the bell's channel at that moment.
	snapshotMu sync.Mutex
	last       *tree
	lastBell   <-chan struct{}
}

func newOperator(namespace string, client *apiclient.Client, dialer indi.Dialer) *operator {
	o := &operator{
		namespace: namespace,
		client:    client,
		changed:   newBell(),
		dialer:    dialer,
		claims:    newClaims(),
		versions:  memo.New(),
		runners:   map[string]*runner{},
		faults:    map[string]string{},
		sites:     map[string]lock{},
	}
	o.servers = newServers(o)
	return o
}

// snapshot reads the stores. A change to a store rings the bell after
// the store holds it, so the stores are the same until the bell rings
// again. Every goroutine wakes on each ring and reads the stores, so
// they share one tree for each ring. A tree converts every object of 22
// collections, and a tree for each reader would repeat that conversion
// in every goroutine on every ring.
func (o *operator) snapshot() *tree {
	ring := o.changed.wait()
	o.snapshotMu.Lock()
	defer o.snapshotMu.Unlock()
	if o.last == nil || o.lastBell != ring {
		o.last, o.lastBell = o.stores.snapshot(o.namespace), ring
	}
	return o.last
}

// siteLock answers the lock of one observatory's server.
func (o *operator) siteLock(name string) lock {
	o.mu.Lock()
	defer o.mu.Unlock()
	l, ok := o.sites[name]
	if !ok {
		l = newLock()
		o.sites[name] = l
	}
	return l
}

func (o *operator) fault(d *device, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err == nil {
		delete(o.faults, d.key())
		return
	}
	o.faults[d.key()] = err.Error()
}

func (o *operator) faultOf(d *device) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.faults[d.key()]
}

// run opens the watches and supervises until ctx ends.
func (o *operator) run(ctx context.Context, watches func(context.Context) *stores) {
	o.stores = watches(ctx)
	var group sync.WaitGroup
	group.Go(func() { o.writeStatuses(ctx) })
	defer func() {
		group.Wait()
		o.servers.stopAll()
		o.waitRunners()
		o.stores.done()
	}()
	seeded := false
	for {
		wake := o.changed.wait()
		if o.stores.ready() {
			t := o.snapshot()
			if !seeded {
				// The holders of each telescope come from the status of
				// each reservation, before any runner can take one.
				o.claims.seed(t)
				seeded = true
			}
			o.supervise(ctx, t)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// supervise starts the runners and the INDI connections that the tree
// needs, and stops the pods that no reservation holds.
func (o *operator) supervise(ctx context.Context, t *tree) {
	for _, r := range t.reservations {
		if finished(r) {
			continue
		}
		o.startRunner(ctx, r)
	}
	o.claims.forgetGone(t)
	o.servers.sync(ctx, t)
	if err := o.sweep(t); err != nil {
		fmt.Fprintf(os.Stderr, "observatory-operator: %v\n", err)
	}
}

// finished reports whether a reservation has no work left: it is
// Released and holds no finalizer, or it is gone from the API server
// apart from its finalizer-free deletion.
func finished(r *observatory.Reservation) bool {
	return r.Status.Phase == observatory.ReservationReleased && !hasFinalizer(r)
}

func (o *operator) startRunner(ctx context.Context, r *observatory.Reservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, running := o.runners[r.Metadata.UID]; running {
		return
	}
	run := newRunner(o, r)
	o.runners[r.Metadata.UID] = run
	go func() {
		run.run(ctx)
		o.mu.Lock()
		delete(o.runners, r.Metadata.UID)
		o.mu.Unlock()
		close(run.done)
		// The next pass starts a runner again if the reservation still
		// has work, such as after a retry.
		o.changed.notify()
	}()
}

func (o *operator) waitRunners() {
	o.mu.Lock()
	var done []chan struct{}
	for _, r := range o.runners {
		done = append(done, r.done)
	}
	o.mu.Unlock()
	for _, d := range done {
		<-d
	}
}

// sweep deletes the pods and Services of each server that no
// reservation holds. A runner deletes them itself at the end of
// deactivation, so the sweep finds them only when a reservation went
// away without deactivation, such as one whose finalizer a person
// removed.
func (o *operator) sweep(t *tree) error {
	held := map[string]bool{}
	for telescope := range o.claims.held() {
		held[serverRef{observatory.TelescopeKind, telescope}.String()] = true
		if scope, ok := t.telescopes[telescope]; ok {
			held[serverRef{observatory.ObservatoryKind, scope.Spec.Observatory}.String()] = true
		}
	}
	var problems []error
	for name, p := range t.pods {
		if server := p.Metadata.Labels[labelServer]; !held[server] && p.Metadata.DeletionTimestamp == nil {
			problems = append(problems, o.deleteObject(podPath(o.namespace, name)))
		}
	}
	for name, s := range t.services {
		if server := s.Metadata.Labels[labelServer]; !held[server] {
			problems = append(problems, o.deleteObject(servicePath(o.namespace, name)))
		}
	}
	return joinErrors(problems)
}

// The pause before a refused write is sent again. It is a backoff
// clock, the one timer around a write: it spaces the tries while the API
// server refuses them, such as while it restarts, and it reads no state.
// It doubles from writeFirst to writeLimit.
const (
	writeFirst = time.Second
	writeLimit = 30 * time.Second
)

// send sends one write until the API server takes it, or until ctx
// ends. report receives each refusal, so the step's message names it,
// and the step's deadline ends the tries.
func (o *operator) send(ctx context.Context, report func(string), what string, write func() error) error {
	pause := writeFirst
	for {
		err := write()
		if err == nil {
			return nil
		}
		message := fmt.Sprintf("%s: %v; sending it again in %v", what, err, pause)
		fmt.Fprintf(os.Stderr, "observatory-operator: %s\n", message)
		if report != nil {
			report(message)
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s: %w", what, err)
		case <-timer.C:
		}
		pause = min(2*pause, writeLimit)
	}
}

// waitFor waits until check reports done, rereading the stores after
// each change of a watch or of an INDI server, or until ctx ends. It
// holds no timer: each wait ends on an event. report receives what
// check says it waits for, each time that text changes.
func (o *operator) waitFor(ctx context.Context, report func(string), check func(*tree) (bool, string, error)) error {
	last := ""
	for {
		wake := o.changed.wait()
		done, waiting, err := check(o.snapshot())
		if done || err != nil {
			return err
		}
		if waiting != last && report != nil {
			report(waiting)
			last = waiting
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-wake:
		}
	}
}

// sleepUntil waits until a time, or until a change, or until ctx ends.
// The timer is a clock: the moment that a reservation's spec names.
func (o *operator) sleepUntil(ctx context.Context, when time.Time) {
	wake := o.changed.wait()
	timer := time.NewTimer(time.Until(when))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-timer.C:
	}
}
