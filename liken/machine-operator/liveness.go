package main

// The heartbeat lease shows that this operator does its work, so a
// stuck reconcile loop must stop renewing it.
//
// cluster-operator marks a machine Lost when its heartbeat lease is
// older than HeartbeatStaleAfter (kubernetes/heartbeat.go). A loop
// blocked on a request that never returns, or on a lock that is never
// released, must not leave the machine looking healthy. So the loop
// marks itself busy when its select returns and idle when it enters the
// select again. A loop that waits in its select is healthy, because it
// acts on the next wake. A separate renewal timer renews the lease
// every renewEvery, unless the loop has been busy for longer than
// stuckAfter.
//
// stuckAfter must be longer than any pass that is not stuck. The
// requests of a pass end at passDeadline (loop.go), and the rest of the
// margin covers work that sends no request, such as a walk of sysfs or
// a syncfs. The metric liken_machine_longest_pass_seconds shows how
// close a machine comes to the limit.
//
// When the loop is stuck, the renewal timer also exits the process. The
// kubelet starts the container again, and its first pass reads the
// current state. When the loop gets stuck after every restart, the
// kubelet's restart backoff grows, the lease ages past
// HeartbeatStaleAfter, and cluster-operator marks the machine Lost.
//
// The process checks itself, and the pod has no kubelet liveness
// probe, because a probe of /healthz also fails when the listener is
// not open: during setup before the loop starts, when
// --metrics-address moves the listener or turns it off, when another
// process has the port, and when the node's imported binary is older
// than the pod template and serves no /healthz. /healthz serves the
// same check for a person.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

const (
	// passDeadline is the time after a pass begins at which any of its
	// requests that is still open is cancelled.
	passDeadline = 45 * time.Second

	// stuckAfter is how long the loop may stay busy before the lease
	// stops renewing and /healthz fails.
	stuckAfter = 60 * time.Second

	// renewEvery is half of HeartbeatRenewAfter. Renew skips a write
	// until the lease is that old, so a tick that comes slightly early
	// and is skipped is followed by a tick that renews.
	renewEvery = kubernetes.HeartbeatRenewAfter / 2
)

// liveness is the state that the reconcile loop and the renewal timer
// share. The fields are atomic because the timer runs on its own
// goroutine.
type liveness struct {
	// start is the reference time for busySince. A duration measured
	// from start uses the monotonic clock, so a change of the wall
	// clock does not make the loop look busy or idle.
	start time.Time

	// busySince is the offset from start at which the loop's select
	// returned, plus one so that zero can mean idle.
	busySince atomic.Int64

	// owner is this machine's Machine, which owns the lease. gone is
	// true while the Machine does not exist, because a renewal would
	// then create a lease owned by a Machine that is absent, and the
	// garbage collector would delete it.
	owner atomic.Pointer[kubernetes.OwnerReference]
	gone  atomic.Bool

	// exit stops the process. main uses os.Exit, and tests use a
	// function that records the call.
	exit func(code int)
}

func newLiveness() *liveness {
	return &liveness{start: time.Now(), exit: os.Exit}
}

// markBusy records that the loop became busy now. It keeps the earlier
// time when the loop is already busy. A nil liveness does nothing.
func (l *liveness) markBusy() {
	if l != nil {
		l.busySince.CompareAndSwap(0, int64(time.Since(l.start))+1)
	}
}

// markIdle records that the loop is waiting for a wake signal.
func (l *liveness) markIdle() {
	if l != nil {
		l.busySince.Store(0)
	}
}

// busyFor returns how long the loop has been busy, or zero while it
// waits.
func (l *liveness) busyFor() time.Duration {
	since := l.busySince.Load()
	if since == 0 {
		return 0
	}
	return time.Since(l.start) - time.Duration(since-1)
}

var errStuck = errors.New("the reconcile loop is stuck")

// check returns an error when the loop has been busy for longer than
// stuckAfter. /healthz serves it.
func (l *liveness) check() error {
	if busy := l.busyFor(); busy > stuckAfter {
		return fmt.Errorf("%w: busy for %s", errStuck, busy.Round(time.Second))
	}
	return nil
}

// sawMachineOf records the Machine that a pass read, as the owner of
// the lease. sawNoMachine records that the Machine does not exist.
func (l *liveness) sawMachineOf(m *machine.Machine) {
	if l == nil {
		return
	}
	l.owner.Store(&kubernetes.OwnerReference{APIVersion: api.APIVersion, Kind: machineKind, Name: m.Metadata.Name, UID: m.Metadata.UID})
	l.gone.Store(false)
}

func (l *liveness) sawNoMachine() {
	if l != nil {
		l.gone.Store(true)
	}
}

// renew renews the lease once, unless the Machine is gone or the loop
// is stuck. After the loop starts, only the renewal timer calls it,
// because Heartbeat has no lock.
func (l *liveness) renew(hb *kubernetes.Heartbeat, c *apiclient.Client) {
	owner := l.owner.Load()
	if owner == nil || l.gone.Load() || l.check() != nil {
		return
	}
	hb.Renew(c, *owner, time.Now())
}

// renewUntil renews the lease every renewEvery until ctx ends, and
// exits the process when the loop is stuck. The caller renews once
// before the first pass, so a machine that boots after the fleet has
// already marked it Lost renews the lease before its first status
// write.
func (l *liveness) renewUntil(ctx context.Context, hb *kubernetes.Heartbeat, c *apiclient.Client) {
	ticker := time.NewTicker(renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.check(); err != nil {
				fmt.Fprintf(os.Stderr, "%v, so the process exits and the kubelet starts it again\n", err)
				l.exit(1)
				return
			}
			l.renew(hb, c)
		}
	}
}
