package main

// The operator runs five kinds of goroutine on the stores of the
// watches (watch.go):
//
//   - The supervisor below starts a runner for each Reservation that
//     has work left, opens an INDI connection to each server pod
//     (indiconn.go) and a connection to each guider's PHD2
//     (guiderconn.go), and deletes the pods of a server that no
//     reservation holds.
//   - One runner for each Reservation runs its steps in order
//     (reservation.go).
//   - The status writer writes the status of every other resource
//     (status.go).
//   - The lock relay sends each park state that a lock policy needs to
//     the other INDI servers (locks.go).
//   - The trigger controller runs the procedures of every trigger in
//     spec.triggers (triggers.go).
//
// They wait on two bells. changed rings on every watch event, every
// INDI event, and every change a guider's PHD2 reports. structure rings
// on the same events except an INDI property's update and a device's
// message, which a mount that tracks sends several times a second, and
// except a PHD2 change other than its connection or its equipment, such
// as a guide step each second. The status writer, the lock relay, and
// the steps that wait for a property's value wait on changed. The
// supervisor, a runner that keeps a Ready telescope, and a runner that
// waits for its turn, its retry, or its end read only the stores and
// the devices that each server defines, so they wait on structure.
// The trigger controller reads only the stored conditions and the
// records of runs and activity, and each change of a record rings
// structure, so it waits on structure too.

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
	changed   *bell
	// structure also rings changed.
	structure *bell
	// dialer opens each INDI connection and each connection to a
	// guider's PHD2. Nil dials the network.
	dialer indi.Dialer
	// logs receives the operator's log (logs.go): standard error in a
	// pod, and a buffer in a test that reads what the log says.
	logs io.Writer
	// recorder posts the Events (events.go). Nil posts none.
	recorder *events.Recorder

	servers *servers
	// guiderConns holds the connection to each guider's PHD2.
	guiderConns *guiderConns
	claims      *claims
	// versions records the version of each reservation that the
	// operator wrote or read last, so a runner never acts on an older
	// copy from a store (informer.ReadOne).
	versions *memo.Versions
	// serverDrivers records the devices that the operator wrote last on
	// each server's pod (serverdrivers.go).
	serverDrivers driversMemo

	// locks records what the lock relay sent (locks.go).
	locks lockMemo

	// runs records the runs of every procedure (runs.go), and activity
	// the Active state of each Telescope and Observatory (activity.go).
	runs     *runRecords
	activity *activity
	// seeded is set once the claims, the runs, and the activity are
	// read from the stored statuses. The status writer writes no status
	// before then, so it never writes an empty record over a stored
	// one.
	seeded atomic.Bool

	mu      sync.Mutex
	runners map[string]*runner
	// running counts the runners' goroutines, so a stop waits for each.
	running sync.WaitGroup
	// faults holds the last failure of each device by its key, such as
	// Camera/east-main, for the device's status.
	faults map[string]string
	// sites serializes the work on each observatory's server, which
	// the runners of several telescopes share.
	sites map[string]lock

	// snapshotMu guards last, the tree that snapshot built, and
	// lastVersion, the version of the stores it read.
	snapshotMu  sync.Mutex
	last        *tree
	lastVersion uint64
}

func newOperator(namespace string, client *apiclient.Client, dialer indi.Dialer) *operator {
	changed := newBell(nil)
	o := &operator{
		namespace: namespace,
		client:    client,
		changed:   changed,
		structure: newBell(changed),
		dialer:    dialer,
		logs:      os.Stderr,
		claims:    newClaims(),
		versions:  memo.New(),
		runners:   map[string]*runner{},
		faults:    map[string]string{},
		sites:     map[string]lock{},
	}
	// A run or an activity that changes rings structure, which also
	// rings changed, so the trigger controller wakes on structure and
	// an INDI reading does not wake it.
	o.runs = newRunRecords(o.structure)
	o.activity = newActivity(o.structure)
	o.servers = newServers(o)
	o.guiderConns = newGuiderConns(o)
	return o
}

// snapshot reads the stores. Every goroutine wakes on each ring of its
// bell and reads the stores, but most rings come from INDI events,
// which change no store. So the goroutines share one tree until a
// store changes (stores.version). A tree reads every object of 22
// collections, and a tree for each ring would repeat that work about
// ten times a second while a mount tracks.
func (o *operator) snapshot() *tree {
	// The version is read before the stores. A change that lands
	// between the two moves the version, so the next call reads the
	// stores again.
	version := o.stores.version.Load()
	o.snapshotMu.Lock()
	defer o.snapshotMu.Unlock()
	if o.last == nil || o.lastVersion != version {
		o.last, o.lastVersion = o.stores.snapshot(o.namespace), version
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
	group.Go(func() { o.keepLocks(ctx) })
	group.Go(func() { o.keepTriggers(ctx) })
	defer func() {
		group.Wait()
		o.servers.stopAll()
		o.guiderConns.stopAll()
		o.running.Wait()
		o.stores.done()
	}()
	seeded := false
	for {
		wake := o.structure.wait()
		if o.stores.ready() {
			t := o.snapshot()
			if !seeded {
				// The holders of each telescope come from the status of
				// each reservation, before any runner can take one, and
				// the runs and the activity come from the stored
				// statuses, before any runner or trigger runs.
				o.claims.seed(t)
				o.runs.seed(t)
				o.activity.seed(t, o.claims)
				seeded = true
				o.seeded.Store(true)
				o.changed.notify()
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
	o.activity.forgetUnheld(t, o.claims)
	o.runs.forget(o.snapshot)
	o.servers.sync(ctx, t)
	o.guiderConns.sync(ctx, t)
	held := o.heldServers(t)
	if err := o.sweep(t, held); err != nil {
		o.logf("%v", err)
	}
	if err := o.releaseDevices(t, held); err != nil {
		o.logf("%v", err)
	}
	if err := o.keepParents(t); err != nil {
		o.logf("%v", err)
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
	o.running.Go(func() {
		run.run(ctx)
		o.mu.Lock()
		delete(o.runners, r.Metadata.UID)
		o.mu.Unlock()
		// The next pass starts a runner again if the reservation still
		// has work, such as after a retry.
		o.structure.notify()
	})
}

// sweep deletes the pods, Services, and ConfigMaps of each server that
// no reservation holds. A runner deletes them itself at the end of
// deactivation, so the sweep finds them only when a reservation went
// away without deactivation, such as one whose finalizer a person
// removed.
func (o *operator) sweep(t *tree, held map[string]bool) error {
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
	for name, files := range t.configMaps {
		if server := files.Metadata.Labels[labelServer]; !held[server] {
			problems = append(problems, o.deleteObject(configMapPath(o.namespace, name)))
		}
	}
	return joinErrors(problems)
}

// heldServers answers the names of the servers that a reservation
// holds: the server of each held telescope and of its observatory.
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
	wake := o.structure.wait()
	timer := time.NewTimer(time.Until(when))
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	case <-timer.C:
	}
}
