package main

// This file runs the reconcile loop and decides what starts each pass.
//
// The loop is level-triggered. Each pass reconciles from the current
// state, not from the event that started it, so a missed event cannot
// leave the machine wrong. A pass starts when:
//
//   - a Kubernetes watch receives a change to an object this machine acts
//     on (watches.go).
//   - init writes under /run/liken/facts, so a new fact such as a time
//     sync reaches status without waiting for a timer.
//   - the kernel reports a device change, or /etc/hosts changes
//     (machineevents.go).
//   - the retry timer fires, because the last pass left a step
//     unfinished or a step asked to run again at a set time (retry.go).
//   - the sysctl check finds that a parameter the last pass applied
//     reads a different value (backstop.go).
//   - the backstop fires, after backstopEvery with no other pass
//     (backstop.go).
//
// The lease renews on its own timer, which stops when the loop is
// stuck (liveness.go).
//
// Each wake channel has a buffer of one, so a burst of changes queues
// one wake signal and one pass over the newest state, as an informer's work
// queue does.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

// loop is the reconcile loop's state across passes and the channels
// that start a pass.
type loop struct {
	objects     *reader
	name        string
	clusterName string
	fetcher     *fetcher
	heartbeat   *kubernetes.Heartbeat
	operator    *metrics.Operator
	layer       *machineMetrics

	// wakes receives a value when a Kubernetes watch receives a change.
	wakes <-chan struct{}

	// uevents and hosts receive the events of the machine's readers
	// (machineevents.go). The loop runs without a reader whose channel
	// is nil.
	uevents <-chan struct{}
	hosts   <-chan struct{}

	// released receives a value after the DRA plugin unprepares a
	// claim. A nil channel means the plugin sends none.
	released <-chan struct{}

	// live records whether the loop is busy, for the lease renewal
	// timer and /healthz (liveness.go). When nil, the loop renews no
	// lease.
	live *liveness

	// backstopJitter returns a number in [0, 1) that sets the random
	// part of the backstop delay. When nil, the loop uses math/rand.
	backstopJitter func() float64

	// watchFactsTree opens the facts watch. It is a field so that a
	// test can pass a fake.
	watchFactsTree func(ctx context.Context) (*factsWatch, error)

	retry retrySchedule
}

// run runs passes until ctx ends, starting from current, the Machine
// that main read or created. It returns an error when a reader stops,
// or when the facts watch fails to open or stops. main exits on that
// error, and machineevents.go explains why.
func (l *loop) run(ctx context.Context, current *machine.Machine) error {
	// Init writes the facts tree before /run/liken/machine.yaml, which
	// main read at startup, so the tree exists here. A failure to open
	// the watch means the machine is broken.
	factsWatch, err := l.watchFactsTree(ctx)
	if err != nil {
		return fmt.Errorf("watching the facts tree: %w", err)
	}

	// Each relay forwards its reader's events to machineWakes.
	// stopped has room for every relay, so a relay that ends after run
	// returns does not block on its send.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	machineWakes := make(chan struct{}, 1)
	stopped := make(chan error, 3)
	if l.uevents != nil {
		go func() {
			stopped <- relayError("the uevent listener", relay(ctx, l.uevents, machineWakes, settleEvents))
		}()
	}
	if l.hosts != nil {
		go func() { stopped <- relayError("the hosts watch", relay(ctx, l.hosts, machineWakes, settleEvents)) }()
	}
	if l.released != nil {
		go func() {
			stopped <- relayError("the DRA plugin's unprepare", relay(ctx, l.released, machineWakes, settleEvents))
		}()
	}

	// Renew the lease once before the first pass, with the Machine that
	// main read as its owner, so a machine that the fleet already marked
	// Lost shows it is alive before its first status write. After this,
	// only the timer renews the lease.
	if l.live != nil {
		l.live.sawMachineOf(current)
		l.live.renew(l.heartbeat, l.objects.client)
		go l.live.renewUntil(ctx, l.heartbeat, l.objects.client)
	}

	// The loop is marked busy only after that renewal, because the
	// renewal's requests have no pass deadline. A slow renewal then
	// cannot make /healthz report a stuck loop.
	l.live.markBusy()
	woke := time.Now()

	var retry *time.Timer
	sysctlChecks := time.NewTicker(sysctlCheckEvery)
	defer sysctlChecks.Stop()
	backstop := time.NewTimer(backstopDelay(l.backstopJitter))
	defer backstop.Stop()
	cause := causeStart
	var sysctls sysctlCheck
	for {
		// inotify does not watch new subdirectories on its own, so sync
		// adds a watch for each directory that init created since the
		// last pass, before this pass reads the tree. When the watch's
		// channel is closed, its file descriptor is closed too, and sync
		// would add watches to a descriptor number that the process may
		// have reused.
		if watchStopped(factsWatch) {
			return errFactsStopped
		}
		// When sync fails, writes in a new directory start no pass, so
		// the failure schedules a retry soon.
		out := &passOutcome{}
		if err := factsWatch.sync(); err != nil {
			fmt.Fprintf(os.Stderr, "syncing the facts watch: %v\n", err)
			out.failSoon("syncing the facts watch", err)
		}
		l.layer.observeWake(cause)

		// passDeadline is shorter than stuckAfter, so a pass with slow
		// requests ends before the lease stops renewing, and only a
		// stuck loop stops it (liveness.go).
		passCtx, endPass := context.WithTimeout(ctx, passDeadline)
		objects := l.objects.throughAPIOnce().within(passCtx)
		// Read the Machine at the start of each pass, because each
		// status write changes its resourceVersion and a stale copy
		// makes the next status write a conflict. When the read fails,
		// the pass uses the previous copy, and publish retries on a
		// conflict.
		//
		// When the Machine is gone, stop renewing the lease. The garbage
		// collector deletes the lease with its owner, the Machine, so a
		// renewal would create a lease that the collector deletes again.
		if fresh, err := objects.observedBy(out).machine(l.name); err == nil {
			current = fresh
			l.live.sawMachineOf(current)
		} else if errors.Is(err, apiclient.ErrNotFound) {
			l.live.sawNoMachine()
		}
		started := time.Now()
		err := reconcile(objects, current, l.clusterName, l.fetcher, l.layer, out)
		took := time.Since(started)
		endPass()
		l.operator.ObserveReconcile(machineKind, took, err)
		// Record the whole busy period that /healthz measures, from the
		// wake to the end of the pass.
		l.layer.observePass(time.Since(woke))
		// Stop each watch of a wait that this pass did not read
		// (waits.go).
		l.objects.waits.endPass()
		if out.sysctls != nil {
			sysctls = sysctlCheck{applied: out.sysctls, missing: out.sysctlsMissing, requestSizes: out.requestSizes}
		}
		if cause == causeBackstop {
			reportRepairs(out.writes, machineEvents{recorder: l.objects.recorder, machine: machineReference(current)}, l.layer)
		}

		// One timer schedules both the retry and each step's requested
		// rerun.
		// Each pass sets it from its own outcome, so a pass that finishes
		// every step leaves it stopped.
		if retry != nil {
			retry.Stop()
		}
		var retryC <-chan time.Time
		if at, ok := l.retry.next(out, time.Now()); ok {
			retry = time.NewTimer(time.Until(at))
			retryC = retry.C
			if len(out.failures) > 0 {
				fmt.Printf("the pass did not finish %s, and retries in %s\n",
					describeUnfinished(out.failures), max(0, time.Until(at)).Round(10*time.Millisecond))
			}
		}
		// The backstop counts from the end of the last pass. It stays
		// stopped while a retry is pending, because a backstop pass
		// reports its writes as repairs of a missing wake signal, and the
		// retry's writes are not.
		backstop.Stop()
		if retryC == nil {
			backstop.Reset(backstopDelay(l.backstopJitter))
		}

		var stop bool
		cause, stop, err = l.wait(ctx, waitSources{stopped: stopped, machineWakes: machineWakes, retry: retryC,
			facts: factsWatch, sysctlChecks: sysctlChecks.C, backstop: backstop.C}, sysctls)
		if stop {
			if retry != nil {
				retry.Stop()
			}
			return err
		}
		woke = time.Now()
	}
}

// The causes of a pass, as labels of the passes_total counter.
const (
	causeStart    = "start"
	causeWatch    = "watch"
	causeMachine  = "machine event"
	causeRetry    = "retry"
	causeFacts    = "facts"
	causeSysctls  = "sysctl drift"
	causeDisks    = "disk drift"
	causeBackstop = "backstop"
)

// waitSources are the channels that wait reads, besides the loop's
// own wakes.
type waitSources struct {
	stopped      <-chan error
	machineWakes <-chan struct{}
	retry        <-chan time.Time
	facts        *factsWatch
	sysctlChecks <-chan time.Time
	backstop     <-chan time.Time
}

// wait blocks until a pass must run and returns its cause. It marks
// the loop busy as soon as a channel fires. When the sysctl check finds
// every parameter unchanged, wait keeps waiting. The boolean is true,
// with the error for run to return, when the loop must end.
func (l *loop) wait(ctx context.Context, from waitSources, sysctls sysctlCheck) (string, bool, error) {
	for {
		l.live.markIdle()
		select {
		case <-ctx.Done():
			return "", true, nil
		case err := <-from.stopped:
			l.live.markBusy()
			if err != nil {
				return "", true, err
			}
			return causeMachine, false, nil
		case <-l.wakes:
			l.live.markBusy()
			return causeWatch, false, nil
		case <-from.machineWakes:
			l.live.markBusy()
			return causeMachine, false, nil
		case <-from.retry:
			l.live.markBusy()
			return causeRetry, false, nil
		case _, ok := <-from.facts.wake:
			l.live.markBusy()
			// A closed channel means the watch stopped, and init's later
			// writes would start no pass.
			if !ok {
				return "", true, errFactsStopped
			}
			return causeFacts, false, nil
		case <-from.backstop:
			l.live.markBusy()
			return l.backstopCause(from, sysctls), false, nil
		case <-from.sysctlChecks:
			l.live.markBusy()
			if sysctls.drifted(sysctlRoot) {
				return causeSysctls, false, nil
			}
			if sysctls.requestSizesDrifted(sysBlockRoot()) {
				return causeDisks, false, nil
			}
		}
	}
}

// backstopCause returns the cause of a pass that the backstop timer
// started. When another cause is ready at the same time, it returns
// that cause, so the pass's writes are not reported as repairs of a
// missing wake signal.
func (l *loop) backstopCause(from waitSources, sysctls sysctlCheck) string {
	select {
	case <-l.wakes:
		return causeWatch
	case <-from.machineWakes:
		return causeMachine
	case <-from.retry:
		return causeRetry
	default:
	}
	if sysctls.drifted(sysctlRoot) {
		return causeSysctls
	}
	if sysctls.requestSizesDrifted(sysBlockRoot()) {
		return causeDisks
	}
	return causeBackstop
}

// factsWatch is the part of a *machine.TreeWatch that the loop uses:
// its wake channel and its sync method.
type factsWatch struct {
	wake <-chan struct{}
	sync func() error
}

var errFactsStopped = errors.New("the facts watch stopped")

// relayError adds the reader's name to a relay's error. It returns nil
// when the relay ended because its context ended.
func relayError(reader string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", reader, err)
}

// watchStopped reports whether the facts watch's channel is closed. It
// consumes a pending wake from an open channel, which loses nothing
// because the pass that follows reads the whole tree.
func watchStopped(w *factsWatch) bool {
	select {
	case _, ok := <-w.wake:
		return !ok
	default:
		return false
	}
}

// describeUnfinished describes the steps a pass did not finish, with
// each one's error, for the pass's log line. It describes the first
// three and counts the rest, because a machine with no sysctl tree
// fails every parameter at once and each step already logged its own
// failure.
func describeUnfinished(failures []passFailure) string {
	const named = 3
	var parts []string
	for _, f := range failures[:min(len(failures), named)] {
		parts = append(parts, fmt.Sprintf("%s unfinished (%s: %v)", f.step, f.kind, f.err))
	}
	if len(failures) > named {
		parts = append(parts, fmt.Sprintf("and %d more", len(failures)-named))
	}
	return strings.Join(parts, "; ")
}
