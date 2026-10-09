package main

// The heartbeat's clock, and how a stuck loop stops it.
//
// cluster-operator marks a machine Lost when its heartbeat lease goes
// unrenewed for 40 seconds (kubernetes/heartbeat.go). The lease must
// prove that this operator does its job, not only that the process
// runs, so a renewal must stop when the reconcile loop is stuck: a call
// that never answers, or a lock that never releases. A loop that waits
// in its select for a wake is healthy, because it acts the moment a
// wake comes. So the loop marks itself busy from the moment its select
// returns until it enters the select again, and a timer of its own
// renews the lease every few seconds unless the loop has been busy for
// longer than stuckAfter.
//
// stuckAfter must be longer than any pass that is not stuck, so the
// pass's requests run under passDeadline (loop.go), and the margin
// covers the work that sends no request, such as a walk of sysfs or a
// syncfs. The gauge of the longest pass shows how close a machine comes
// to the limit.
//
// A loop that is stuck stops the renewals, and the same timer ends the
// process, the crash-only rule at the head of main.go: the kubelet
// starts the container again, and its first pass reads the state as it
// is. A process that cannot restart its loop keeps restarting, and the
// kubelet's growing backoff lets the lease age past 40 seconds, so
// cluster-operator marks the machine Lost. The timer is in the process,
// not a kubelet liveness probe, because a probe also fails while the
// listener is not open: during the setup before the loop, when port
// 9200 is taken or moved, and for a binary older than its pod
// template. /healthz answers the same check, for a person.

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
	// passDeadline ends every request a pass sends that is still open
	// this long after the pass began.
	passDeadline = 45 * time.Second

	// stuckAfter is how long the loop may stay busy before the
	// renewals stop and /healthz fails.
	stuckAfter = 60 * time.Second

	// renewEvery is half of HeartbeatRenewAfter, so a timer that fires
	// a moment early, and finds the last renewal a moment short of the
	// age that Renew wants, renews on its next firing.
	renewEvery = kubernetes.HeartbeatRenewAfter / 2
)

// liveness is what the loop and the renewal timer share. Each value is
// atomic, because the timer runs on its own goroutine.
type liveness struct {
	// start is the reference for busySince. A duration since start
	// reads the monotonic clock, so a step of the wall clock does not
	// make the loop look busy or idle.
	start time.Time

	// busySince is the time since start at which the loop's select
	// returned, plus one, or zero while the loop waits in its select.
	busySince atomic.Int64

	// owner is this machine's Machine, the owner of the lease. gone is
	// true while the Machine does not exist: a renewal would create
	// the lease again, owned by a Machine that does not exist, for the
	// garbage collector to delete again.
	owner atomic.Pointer[kubernetes.OwnerReference]
	gone  atomic.Bool

	// exit ends the process. main passes os.Exit, and a test passes a
	// function that records the call.
	exit func(code int)
}

func newLiveness() *liveness {
	return &liveness{start: time.Now(), exit: os.Exit}
}

// markBusy marks the loop busy from now, unless it is busy already. A
// nil liveness marks nothing.
func (l *liveness) markBusy() {
	if l != nil {
		l.busySince.CompareAndSwap(0, int64(time.Since(l.start))+1)
	}
}

// markIdle marks the loop as waiting for a wake.
func (l *liveness) markIdle() {
	if l != nil {
		l.busySince.Store(0)
	}
}

// busyFor answers how long the loop has been busy, or zero while it
// waits.
func (l *liveness) busyFor() time.Duration {
	since := l.busySince.Load()
	if since == 0 {
		return 0
	}
	return time.Since(l.start) - time.Duration(since-1)
}

// errStuck is the answer of a check of a loop that is stuck.
var errStuck = errors.New("the reconcile loop is stuck")

// check answers an error when the loop has been busy past stuckAfter.
// It is /healthz.
func (l *liveness) check() error {
	if busy := l.busyFor(); busy > stuckAfter {
		return fmt.Errorf("%w: busy for %s", errStuck, busy.Round(time.Second))
	}
	return nil
}

// sawMachineOf records the Machine a pass read, and sawNoMachine a
// Machine that is gone.
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
// is stuck. Only the renewal timer calls it after the loop starts,
// because a Heartbeat has no lock.
func (l *liveness) renew(hb *kubernetes.Heartbeat, c *apiclient.Client) {
	owner := l.owner.Load()
	if owner == nil || l.gone.Load() || l.check() != nil {
		return
	}
	hb.Renew(c, *owner, time.Now())
}

// renewUntil renews the lease every renewEvery until ctx ends, and
// ends the process once the loop is stuck. The caller renews once
// before it starts the loop's first pass, so a machine that boots into
// a fleet that already declared it Lost announces itself before its
// first status write.
func (l *liveness) renewUntil(ctx context.Context, hb *kubernetes.Heartbeat, c *apiclient.Client) {
	ticker := time.NewTicker(renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.check(); err != nil {
				fmt.Fprintf(os.Stderr, "%v; ending the process so the kubelet starts it again\n", err)
				l.exit(1)
				return
			}
			l.renew(hb, c)
		}
	}
}
