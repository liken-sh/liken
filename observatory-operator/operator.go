package main

// An operator runs the observatory of one namespace. namespaces.go
// starts one for each namespace that has objects. Each operator runs
// these goroutines, which read the stores that the watches fill
// (watch.go):
//
//   - The supervisor (run and supervise below) starts a runner for each
//     Reservation that has work left, opens an INDI connection to each
//     server pod (indiconn.go) and a connection to each guider's PHD2
//     (guiderconn.go), and deletes the pods of a server that no
//     Reservation holds.
//   - A runner for each Reservation executes its steps in order
//     (reservation.go).
//   - The status writer writes the status of every other resource
//     (status.go).
//   - The lock relay copies the park state that a lock policy needs to
//     the other INDI servers (locks.go).
//   - The trigger controller runs the procedures of the triggers in
//     spec.triggers (triggers.go).
//
// Each goroutine waits on one of two change notifiers (watch.go) and
// reads the stores again when it is notified. changed is notified on
// every watch event, every INDI event, and every change that a
// guider's PHD2 reports. structure is notified on the same events
// except the ones that arrive often: an update to an INDI property, an
// INDI message, and a PHD2 change that leaves the connection and the
// equipment as they were, such as a guide step. Each notification of
// structure also notifies changed.
//
// The status writer, the lock relay, and a step that waits for a
// property value wait on changed, because they show or test property
// values. The supervisor, the trigger controller, and a runner that
// waits for a turn, a retry, an end, or a Ready telescope wait on
// structure, because nothing they read changes on a property update.
// So a tracking mount that reports its position many times a second
// does not wake them.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type operator struct {
	namespace string
	client    *apiclient.Client
	stores    *stores
	changed   *changeNotifier
	structure *changeNotifier
	// dialer opens the INDI connections and the connections to each
	// guider's PHD2. Nil dials the network.
	dialer indi.Dialer
	// logs receives the operator's log (logs.go): standard error in a
	// pod, a buffer in a test.
	logs io.Writer
	// recorder posts the Events (events.go). Nil posts none.
	recorder *events.Recorder
	// series sets the namespace's Prometheus series (series.go).
	series *namespaceSeries

	servers     *servers
	guiderConns *guiderConns
	claims      *claims
	// versions records the newest version of each reservation that the
	// operator wrote or read, so a runner never acts on an older copy
	// from a store (informer.ReadOne).
	versions *memo.Versions
	// serverDrivers records the devices that the operator last wrote
	// to each server's pod (serverdrivers.go).
	serverDrivers driversMemo

	// locks records what the lock relay last sent (locks.go).
	locks lockMemo

	// runs records the runs of every procedure (runs.go). activity
	// records the Active state of each Telescope and Observatory
	// (activity.go).
	runs     *runRecords
	activity *activity
	// loaded is set once claims, runs, and activity are loaded from the
	// stored statuses. The status writer and the trigger controller
	// wait for it, so the status writer never overwrites a stored
	// record with an empty one.
	loaded atomic.Bool

	mu      sync.Mutex
	runners map[string]*runner
	// running counts the runners' goroutines, so a stop waits for each.
	running sync.WaitGroup
	// faults holds the last failure of each device, by device key such
	// as Camera/east-main, for the device's status.
	faults map[string]string
	// interfaceChecks caches, by resource key, whether the resource's INDI
	// device serves the resource's interface (interfaces.go). The
	// cache is built from what the device defined and from what the
	// operator wrote to it.
	interfaceChecks map[string]interfaceCheck
	// sites holds one lock for each observatory's server, so that the
	// runners of several telescopes work on the shared server one at a
	// time.
	sites map[string]lock

	// snapshotMu guards last, the tree that snapshot built, and
	// lastVersion, the stores version that the tree was built from.
	snapshotMu  sync.Mutex
	last        *tree
	lastVersion uint64
}

func newOperator(namespace string, client *apiclient.Client, dialer indi.Dialer) *operator {
	changed := newChangeNotifier(nil)
	o := &operator{
		namespace:       namespace,
		client:          client,
		changed:         changed,
		structure:       newChangeNotifier(changed),
		dialer:          dialer,
		logs:            os.Stderr,
		claims:          newClaims(),
		versions:        memo.New(),
		runners:         map[string]*runner{},
		faults:          map[string]string{},
		interfaceChecks: map[string]interfaceCheck{},
		sites:           map[string]lock{},
	}
	// The trigger controller waits on structure, so a change to a run or
	// to activity notifies structure.
	o.runs = newRunRecords(o.structure)
	o.activity = newActivity(o.structure)
	o.servers = newServers(o)
	o.guiderConns = newGuiderConns(o)
	return o
}

// snapshot returns a tree of every object in the stores. Most
// notifications come from INDI events, which change no store, so snapshot caches the
// tree and builds it again only when stores.version changes.
func (o *operator) snapshot() *tree {
	// Read the version before the stores. A store change between the
	// two reads moves the version, so the next call builds the tree
	// again.
	version := o.stores.version.Load()
	o.snapshotMu.Lock()
	defer o.snapshotMu.Unlock()
	if o.last == nil || o.lastVersion != version {
		o.last, o.lastVersion = o.stores.snapshot(o.namespace), version
	}
	return o.last
}

// siteLock returns the lock for the server of the named observatory.
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

// run supervises the namespace's observatory until ctx ends.
// namespaces.go owns the stores and notifies structure on each store
// change.
func (o *operator) run(ctx context.Context, s *stores) {
	o.stores = s
	var group sync.WaitGroup
	group.Go(func() { o.writeStatuses(ctx) })
	group.Go(func() { o.keepLocks(ctx) })
	group.Go(func() { o.keepTriggers(ctx) })
	defer func() {
		group.Wait()
		o.servers.stopAll()
		o.guiderConns.stopAll()
		o.running.Wait()
		o.series.forget()
	}()
	loaded := false
	for {
		wake := o.structure.wait()
		if o.stores.ready() {
			t := o.snapshot()
			if !loaded {
				// Load the telescope holders from the reservation statuses
				// before any runner can claim a telescope, and load the
				// runs and activity from the stored statuses before any
				// runner or trigger runs.
				o.claims.load(t)
				o.runs.load(t)
				o.activity.load(t, o.claims)
				loaded = true
				o.loaded.Store(true)
				o.changed.notify()
			}
			began := time.Now()
			err := o.supervise(ctx, t)
			o.series.m.observePass(observatory.ReservationKind.Name, time.Since(began), err != nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// supervise starts the runners, the INDI connections, and the PHD2
// connections that the tree needs, and deletes the objects of servers
// that no reservation holds. It logs each failure and returns them
// joined, so that run counts the pass as failed.
func (o *operator) supervise(ctx context.Context, t *tree) error {
	for _, r := range t.reservations {
		if finished(r) && r.Status.ObservedGeneration == r.Metadata.Generation {
			continue
		}
		o.startRunner(ctx, r)
	}
	o.claims.forgetGone(t)
	o.activity.forgetUnheld(t, o.claims)
	o.runs.forget(o.snapshot)
	o.servers.sync(ctx, t)
	o.guiderConns.sync(ctx, t)
	held := o.heldServers(t)
	problems := []error{o.sweep(t, held), o.releaseDevices(t, held), o.syncParentFinalizers(t)}
	for _, err := range problems {
		if err != nil {
			o.logf("%v", err)
		}
	}
	return joinErrors(problems)
}

// finished reports whether a reservation has no steps left: its phase
// is Released and it has no finalizer. supervise still starts a runner
// for a finished reservation that a person edited, so that the runner
// records the new observedGeneration and stops (runner.run).
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
	o.running.Go(func() {
		run.run(ctx)
		o.mu.Lock()
		delete(o.runners, r.Metadata.UID)
		o.mu.Unlock()
		// The next pass starts a new runner if the reservation still has
		// work, such as after a retry.
		o.structure.notify()
	})
}

// sweep deletes the pods, claims, Services, and ConfigMaps of each
// server that no reservation holds. A runner deletes them at the end
// of deactivation, so sweep finds objects only when a reservation went
// away without deactivating, such as when a person removed its
// finalizer. Like stopServer, it deletes the device objects before the
// server's own, so that no device container restarts while its server
// stops. It does not retry a refused delete, because the next pass
// finds the object again.
func (o *operator) sweep(t *tree, held map[string]bool) error {
	unheld := func(l map[string]string) bool { return !held[l[labelServer]] }
	servers := func(l map[string]string) bool { return unheld(l) && l[labelRole] == roleServer }
	var problems []error
	for _, object := range append(t.selectedObjects(o.namespace, withoutServers(unheld)), t.selectedObjects(o.namespace, servers)...) {
		problems = append(problems, o.deleteObject(object.path))
	}
	return joinErrors(problems)
}

// heldServers returns the names of the servers that a reservation
// holds: the server of each held telescope and of that telescope's
// observatory.
func (o *operator) heldServers(t *tree) map[string]bool {
	held := map[string]bool{}
	for telescope := range o.claims.held() {
		held[serverRef{observatory.TelescopeKind, telescope}.String()] = true
		if scope, ok := t.telescopes[telescope]; ok {
			held[serverRef{observatory.ObservatoryKind, scope.Spec.Observatory}.String()] = true
		}
	}
	return held
}

// After a refused write, send waits writeFirst, and doubles the wait up
// to writeLimit for each further refusal. The wait only spaces the
// retries while the API server refuses writes, such as during its
// restart, and reads no state.
const (
	writeFirst = time.Second
	writeLimit = 30 * time.Second
)

// send calls write until it succeeds or ctx ends. When ctx ends first,
// it returns the last error, prefixed with what. report, when not nil,
// receives each retry message so that the step's status shows it.
func (o *operator) send(ctx context.Context, report func(string), what string, write func() error) error {
	pause := writeFirst
	for {
		err := write()
		if err == nil {
			return nil
		}
		message := fmt.Sprintf("retrying in %s: %s: %v", duration(pause), what, err)
		o.logf("%s", message)
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

// waitFor calls check on a fresh snapshot each time changed is notified,
// until check reports done or an error, or ctx ends. It sets no timer
// of its own, so a deadline comes from ctx. report, when not nil,
// receives check's description of what it waits for, each time that
// description changes.
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

// sleepUntil waits until when, until structure is notified, or until ctx
// ends. The timer is a clock for a time that a reservation's spec
// sets.
func (o *operator) sleepUntil(ctx context.Context, when time.Time) {
	wake := o.structure.wait()
	timer := time.NewTimer(time.Until(when))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-timer.C:
	}
}

// awaitChangeOr waits for wake to fire, for ctx to end, or for when to
// pass. A zero when sets no time limit. The timer is a clock for the
// dawn that ends a gate's wait (requires.go).
func awaitChangeOr(ctx context.Context, wake <-chan struct{}, when time.Time) {
	var at <-chan time.Time
	if !when.IsZero() {
		timer := time.NewTimer(time.Until(when))
		defer timer.Stop()
		at = timer.C
	}
	select {
	case <-ctx.Done():
	case <-wake:
	case <-at:
	}
}
