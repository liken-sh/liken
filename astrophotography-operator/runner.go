package main

// The runner of one Reservation. observatory-operator prepares the
// telescope for the holder of a Reservation. While the reservation is
// Ready, its runner connects to the telescope's INDI server as a
// client. The runner finishes any exposure that a restart interrupted,
// chooses a target, claims it, and images it until the target has no
// work left. Then it chooses the next target.
//
// The runner keeps no state that it cannot rebuild. The record of a
// night is in the targets' statuses and in the outbox, so a runner that
// starts again after an operator restart or a lost connection reads
// its progress from them.

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

// retryWait is how long the runner waits after a failed pass, such as
// a write that the API server refused, before it tries again. A change
// to a watched object ends the wait sooner.
const retryWait = 30 * time.Second

type runner struct {
	o         *operator
	namespace string
	name      string
	// res is the reservation as it was when it became Ready. Its session
	// does not change while the reservation is Ready, and the supervisor
	// stops the runner when the reservation leaves Ready.
	res         *observatory.Reservation
	client      *indi.Client
	centering   centering
	guiding     guiding
	nextRise    time.Time
	idle        bool
	warnedNoBox bool
}

func newRunner(o *operator, res *observatory.Reservation) *runner {
	return &runner{
		o: o, namespace: res.Metadata.Namespace, name: res.Metadata.Name, res: res,
		centering: o.centering, guiding: o.guiding,
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

// run works until ctx ends. The supervisor cancels ctx when the
// reservation leaves Ready, and the operator cancels it when it stops.
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

// pass finishes an interrupted exposure, claims the next eligible
// target, and images it. It returns true after it imaged a target, so
// the next pass starts at once.
func (r *runner) pass(ctx context.Context) (bool, error) {
	if r.res.Status.Session.Outbox == nil {
		if !r.warnedNoBox {
			r.o.recorder.Warning(reservationRef(r.res), reasonNoOutbox,
				"The reservation has no spec.outbox, so the operator takes no frames. A frame must be saved to a claim that outlives the camera's pod")
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
				fmt.Sprintf("No Target in namespace %s is eligible, so the telescope waits", r.namespace))
			r.idle = true
		}
		return false, err
	}
	r.idle = false
	return true, r.image(ctx, t)
}

func (r *runner) connect(ctx context.Context) {
	r.keepOpen(ctx, "the INDI connection", r.client.Run)
}

// keepOpen runs a connection again each time it ends, until ctx ends.
// The wait between connections starts at one second and doubles up to
// one minute, and resets after a connection that lasted longer than a
// minute. So a server that restarts is reached again within seconds,
// and a server that is down is not dialed in a tight loop.
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

// forever is the wait for a runner that has no scheduled work. Only a
// wake or the end of the context ends it.
const forever = time.Duration(1 << 62)

// sleep waits until d passes, wake receives, or ctx ends. It returns
// false when ctx ended. A nil wake channel never wakes it.
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

// lostConnection reports whether err means that the INDI connection
// ended or that the server deleted a driver's properties.
func lostConnection(err error) bool {
	return errors.Is(err, indi.ErrDisconnected) || errors.Is(err, indi.ErrNotConnected) || errors.Is(err, indi.ErrDeleted)
}

// readyIn returns the names of the Ready reservations in a namespace.
// A target whose status.reservation names a reservation that is not in
// this set has a claim that ended.
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

// isReady reports whether the reservation is Ready, is not being
// deleted, and has the session that observatory-operator records
// before it sets the phase to Ready.
func isReady(res *observatory.Reservation) bool {
	return res.Status.Phase == observatory.ReservationReady && res.Metadata.DeletionTimestamp == nil && res.Status.Session != nil
}

func targetName(t *astrophotography.Target) string {
	return t.Metadata.Namespace + "/" + t.Metadata.Name
}
