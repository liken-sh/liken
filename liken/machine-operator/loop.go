package main

// The reconcile loop: what wakes a pass, and what the loop keeps from
// one pass to the next.
//
// The core of every operator is a level-triggered loop. Every pass
// reconciles from the current state as it is, never from the event
// that woke it, so missing one wake can never matter. Four things wake
// it, and two timers check what no event reports. The Kubernetes
// watches wake the loop when an object this machine
// acts on changes, so a conductor's grant or a person's edit is acted
// on at once (watches.go names each watch and what wakes it). The facts
// watch wakes the loop when init publishes a change under
// /run/liken/facts, so a fresh fact like a time sync reaches status
// without waiting on a timer. The uevent listener and the hosts watch
// wake the loop when a device or /etc/hosts changes on the machine
// (machineevents.go). The retry timer wakes the loop when the last
// pass left a step unfinished, or when a step asked to run again at a
// set time (retry.go). The check of the sysctls runs a pass when a
// parameter that the last pass applied reads another value, and the
// backstop runs a pass after five minutes with no other pass
// (backstop.go). A settled machine runs no pass until something
// changes. The heartbeat lease renews on a timer of its own, which
// stops when the loop is stuck (liveness.go).
//
// The wake channel has one slot, so a burst of changes (the
// conductor's grant, the sweeper's verdict, this operator's own
// publishes echoing back) makes one wake, and one pass over the
// newest state answers the whole burst. That is what level-triggered
// means, and it is the same merging an informer's work queue does.

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

// loop holds what the reconcile loop keeps across passes, and the
// channels that wake it. main builds one, and a test builds one with
// its own channels.
type loop struct {
	objects     *reader
	name        string
	clusterName string
	fetcher     *fetcher
	heartbeat   *kubernetes.Heartbeat
	operator    *metrics.Operator
	layer       *machineMetrics

	// wakes carries the watches' wakes.
	wakes <-chan struct{}

	// uevents and hosts are the machine's readers (machineevents.go),
	// opened before the loop starts. A nil channel is a reader that
	// did not open, and the loop runs without it.
	uevents <-chan struct{}
	hosts   <-chan struct{}

	// live is the busy mark that the renewal timer reads
	// (liveness.go). Nil renews no lease.
	live *liveness

	// backstopJitter answers a number in [0, 1) for the backstop's
	// delay. Nil means math/rand.
	backstopJitter func() float64

	// watchFactsTree opens the facts watch. main passes
	// machine.WatchFactsTree, and a test passes a watch it controls.
	watchFactsTree func(ctx context.Context) (*factsWatch, error)

	retry retrySchedule
}

// run runs passes until ctx ends, starting from current, the Machine
// that main read or created. It answers an error when a reader stops
// or the facts watch cannot open, and main ends the process on it
// (machineevents.go gives the reason).
func (l *loop) run(ctx context.Context, current *machine.Machine) error {
	// The facts watch turns init's writes into wakes. inotify does not
	// recurse, so the watch reconciles its set with the tree before
	// every read (Sync, below). init publishes the facts before it
	// writes /run/liken/machine.yaml and before it starts k3s, and
	// main exits when machine.yaml is missing, so the tree exists by
	// now, and a watch that cannot open means something is wrong with
	// the machine.
	factsWatch, err := l.watchFactsTree(ctx)
	if err != nil {
		return fmt.Errorf("watching the facts tree: %w", err)
	}

	// The relays turn the machine's events into wakes on a channel of
	// their own, which the select below reads beside the watches'.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	machineWakes := make(chan struct{}, 1)
	stopped := make(chan error, 2)
	if l.uevents != nil {
		go func() {
			stopped <- relayStopped("the uevent listener", relay(ctx, l.uevents, machineWakes, settleEvents))
		}()
	}
	if l.hosts != nil {
		go func() { stopped <- relayStopped("the hosts watch", relay(ctx, l.hosts, machineWakes, settleEvents)) }()
	}

	// The lease renews once before the first pass, with the owner from
	// the Machine that main read, so a machine that boots into a fleet
	// that already declared it Lost announces itself before its first
	// status write. After that only the timer renews it.
	if l.live != nil {
		l.live.sawMachineOf(current)
		l.live.renew(l.heartbeat, l.objects.client)
		go l.live.renewUntil(ctx, l.heartbeat, l.objects.client)
	}

	// The loop is busy until it first waits for a wake. The first
	// renewal above runs before the mark, because its requests run under
	// no pass's deadline (liveness.go).
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
		// Sync before the read closes the window between a new subtree
		// and the watch on it: a directory that init created since the
		// last pass gets a watch now, before this pass reads the tree.
		//
		// A watch whose channel closed has a closed descriptor, and a
		// Sync on it would add watches to a descriptor number the
		// process may have given to something else since.
		if watchStopped(factsWatch) {
			return errFactsStopped
		}
		// A Sync that fails leaves a new subtree with no watch, so its
		// writes would wake nothing. The failure goes to the outcome, and
		// the retry runs Sync again soon.
		out := &passOutcome{}
		if err := factsWatch.sync(); err != nil {
			fmt.Fprintf(os.Stderr, "syncing the facts watch: %v\n", err)
			out.failSoon("syncing the facts watch", err)
		}
		l.layer.observeWake(cause)

		// Every request of the pass ends at passDeadline, so a pass that
		// is slow but not stuck finishes inside stuckAfter, and only a
		// pass that is stuck stops the heartbeat (liveness.go).
		passCtx, endPass := context.WithTimeout(ctx, passDeadline)
		objects := l.objects.throughAPIOnce().within(passCtx)
		// Each pass starts from the newest copy of this machine's
		// object. Status writes change resourceVersion, and
		// reconciling against a stale copy would make every status
		// update a conflict. A read that fails keeps the copy the last
		// pass had; the publish conflict retry handles a stale one.
		//
		// A Machine that is gone gets no heartbeat. The lease names the
		// Machine as its owner, so the garbage collector deletes the
		// lease with it, and a renewal would create the lease again,
		// owned by a Machine that does not exist, for the collector to
		// delete again.
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
		// The gauge times the whole busy window that the liveness check
		// judges, from the wake to the end of the pass.
		l.layer.observePass(time.Since(woke))
		// A wait's watch runs while the passes read it (waits.go).
		l.objects.waits.endPass()
		if out.sysctls != nil {
			sysctls = sysctlCheck{applied: out.sysctls, missing: out.sysctlsMissing}
		}
		if cause == causeBackstop {
			reportRepairs(out.writes, machineEvents{recorder: l.objects.recorder, machine: machineReference(current)}, l.layer)
		}

		// One timer serves the retry and every step's wake, and each
		// pass sets it again from its own outcome, so a pass that
		// finished everything stops it.
		if retry != nil {
			retry.Stop()
		}
		var retryC <-chan time.Time
		if at, ok := l.retry.next(out, time.Now()); ok {
			retry = time.NewTimer(time.Until(at))
			retryC = retry.C
			if len(out.failures) > 0 {
				fmt.Printf("the pass left %s; a retry is due in %s\n",
					describeUnfinished(out.failures), max(0, time.Until(at)).Round(10*time.Millisecond))
			}
		}
		// The backstop counts from the end of the last pass, so a
		// machine that runs passes for other reasons runs no backstop.
		// While a retry is due, the retry's pass does the backstop's work,
		// and a write it makes is the retry's, not a missed wake.
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

// The causes of a pass, for the passes_total counter, so each pass on
// a settled machine can be explained.
const (
	causeStart    = "start"
	causeWatch    = "watch"
	causeMachine  = "machine event"
	causeRetry    = "retry"
	causeFacts    = "facts"
	causeSysctls  = "sysctl drift"
	causeBackstop = "backstop"
)

// waitSources are the channels the loop's select reads.
type waitSources struct {
	stopped      <-chan error
	machineWakes <-chan struct{}
	retry        <-chan time.Time
	facts        *factsWatch
	sysctlChecks <-chan time.Time
	backstop     <-chan time.Time
}

// wait waits in the loop's select until something asks for a pass, and
// answers its cause. It marks the loop busy from the moment the select
// returns. A check of the sysctls that finds every parameter as the
// last pass left it goes back to the select with no pass. It answers
// true, with the error for run to return, when the loop must end.
//
// A backstop that fires while another cause is ready gives the pass to
// that cause, so its writes are not reported as a missed wake: a wake
// already waiting, and a sysctl that drifted, explain the pass better.
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
			// A closed channel means the watch died, and init's writes
			// since then reach nobody.
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
		}
	}
}

// backstopCause answers the cause of a pass that the backstop's timer
// started: another cause that is ready at the same moment, or the
// backstop itself.
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
	return causeBackstop
}

// factsWatch is the part of the facts watch the loop uses: the wake
// channel and the Sync of a *machine.TreeWatch.
type factsWatch struct {
	wake <-chan struct{}
	sync func() error
}

// errFactsStopped ends the loop when the facts watch's channel
// closes.
var errFactsStopped = errors.New("the facts watch stopped")

// relayStopped names the reader in a relay's error, and passes the nil
// of a relay whose context ended.
func relayStopped(reader string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", reader, err)
}

// watchStopped answers whether the facts watch's channel is closed. A
// wake still pending on an open channel is consumed, which costs
// nothing, because the pass that follows reads the whole tree.
func watchStopped(w *factsWatch) bool {
	select {
	case _, ok := <-w.wake:
		return !ok
	default:
		return false
	}
}

// describeUnfinished names the steps a pass did not finish, with the
// error each met, for the one log line a pass with failures writes. It
// names the first three and counts the rest, because a machine whose
// sysctl tree is missing fails every parameter at once, and the step
// itself already logged each one.
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
