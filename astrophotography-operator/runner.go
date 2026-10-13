package main

// The runner of one Reservation (plan 01). observatory-operator readies
// the telescope for the holder that a Reservation names, and while a
// reservation of this operator is Ready, its runner drives the
// telescope's INDI server as a client: it finishes the exposure that a
// restart interrupted, chooses a target, claims it, and images it until
// the target has no work left. Then it chooses the next one.
//
// The runner holds nothing it cannot read again. The record of a night
// is in the targets' statuses and in the outbox, so a runner that
// starts again, after a restart of the operator or a lost connection,
// reads where its work stands from them.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// retryWait is how long the runner waits after a pass that failed, such
// as a write the API server refused, before it tries again. A change to
// a watched object wakes it sooner.
const retryWait = 30 * time.Second

type runner struct {
	o         *operator
	namespace string
	name      string
	// res is the reservation as it was when it turned Ready. Its session
	// is a record that does not change while the reservation is Ready,
	// and the supervisor stops the runner when the phase changes.
	res       *observatory.Reservation
	client    *indi.Client
	centering centering
	guiding   guiding
	// skipped holds the generation of each target, by UID, that failed
	// on this reservation, so the runner does not take it again until a
	// person edits it. A restart forgets it, and costs one more try.
	skipped     map[string]int64
	nextRise    time.Time
	idle        bool
	warnedNoBox bool
}

func newRunner(o *operator, res *observatory.Reservation) *runner {
	return &runner{
		o: o, namespace: res.Metadata.Namespace, name: res.Metadata.Name, res: res,
		centering: o.centering, guiding: o.guiding,
		skipped: map[string]int64{},
	}
}

func (r *runner) logf(format string, args ...any) {
	fmt.Fprintf(r.o.logs, "%s: reservation %s/%s: %s\n", managedBy, r.namespace, r.name, fmt.Sprintf(format, args...))
}

func reservationRef(res *observatory.Reservation) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: observatory.APIVersion, Kind: observatory.ReservationKind.Name,
		Namespace: res.Metadata.Namespace, Name: res.Metadata.Name, UID: res.Metadata.UID,
	}
}

// run works until ctx ends. The supervisor ends it when the
// reservation leaves Ready, and the operator ends it when it stops.
func (r *runner) run(ctx context.Context) {
	address, err := indiAddress(r.res)
	if err != nil {
		r.logf("%v", err)
		return
	}
	r.client = indi.NewClient(address, indi.WithDialer(r.o.indiDialer))
	connected := make(chan struct{})
	go func() {
		defer close(connected)
		r.connect(ctx)
	}()
	defer func() { <-connected }()
	keepGuider := r.guiding.open(r)
	guider := make(chan struct{})
	go func() {
		defer close(guider)
		keepGuider(ctx)
	}()
	defer func() { <-guider }()
	for ctx.Err() == nil {
		wake := r.o.changed.wait()
		again, err := r.pass(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			r.logf("%v", err)
			sleep(ctx, retryWait, wake)
		case !again:
			sleep(ctx, r.riseWait(), wake)
		}
	}
}

// pass does one piece of work, and answers whether more work waits at
// once.
func (r *runner) pass(ctx context.Context) (bool, error) {
	if r.res.Status.Session.Outbox == nil {
		if !r.warnedNoBox {
			r.o.recorder.Warning(reservationRef(r.res), reasonNoOutbox,
				"The reservation names no spec.outbox, so astrophotography-operator images nothing: a frame must land in a claim that outlives the camera's pod.")
			r.warnedNoBox = true
		}
		return false, nil
	}
	if err := r.resume(ctx); err != nil {
		return false, err
	}
	t, err := r.claimNext()
	if err != nil || t == nil {
		if err == nil && !r.idle {
			r.o.recorder.Normal(reservationRef(r.res), reasonIdle,
				fmt.Sprintf("No Target in namespace %s is eligible, so the telescope waits.", r.namespace))
			r.idle = true
		}
		return false, err
	}
	r.idle = false
	return true, r.image(ctx, t)
}

// connect keeps the INDI connection open until ctx ends.
func (r *runner) connect(ctx context.Context) {
	r.keepOpen(ctx, "the INDI connection", r.client.Run)
}

// keepOpen runs one connection after another until ctx ends. A
// connection that ends opens again after a backoff, which a connection
// that lasted resets, so a server that restarts is reached again within
// seconds, and a server that is down is not dialed in a tight loop.
func (r *runner) keepOpen(ctx context.Context, what string, run func(context.Context) error) {
	const first, most = time.Second, time.Minute
	backoff := first
	for {
		opened := time.Now()
		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		r.logf("%s ended: %v", what, err)
		if time.Since(opened) > most {
			backoff = first
		}
		if !sleep(ctx, backoff, nil) {
			return
		}
		backoff = min(2*backoff, most)
	}
}

// forever is a wait that only a wake or the end of a context ends.
const forever = time.Duration(1 << 62)

// sleep waits for d, for the wake channel, or for the end of ctx, and
// answers false when ctx ended. A nil wake never wakes it.
func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-wake:
	case <-ctx.Done():
		return false
	}
	return true
}

// lostConnection reports whether an error of the INDI client means
// that the connection ended, or that a driver went with its
// properties, so the work waits for the server and goes on.
func lostConnection(err error) bool {
	return errors.Is(err, indi.ErrDisconnected) || errors.Is(err, indi.ErrNotConnected) || errors.Is(err, indi.ErrDeleted)
}

// readyIn answers the Reservations of this operator in a namespace
// whose phase is Ready, by name. A target whose status.reservation
// names any other claims nothing.
func (o *operator) readyIn(namespace string) map[string]bool {
	view := o.stores.collections[observatory.ReservationKind.Name].View()
	ready := map[string]bool{}
	for _, res := range informer.CachedList[observatory.Reservation](view) {
		if res.Metadata.Namespace == namespace && isReady(&res) {
			ready[res.Metadata.Name] = true
		}
	}
	return ready
}

// isReady reports whether a reservation is Ready, with the session that
// observatory-operator records before it turns Ready.
func isReady(res *observatory.Reservation) bool {
	return res.Status.Phase == observatory.ReservationReady && res.Metadata.DeletionTimestamp == nil && res.Status.Session != nil
}

// targetName names a target in a message.
func targetName(t *astrophotography.Target) string {
	return t.Metadata.Namespace + "/" + t.Metadata.Name
}
