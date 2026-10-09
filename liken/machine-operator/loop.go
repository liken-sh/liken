package main

// The reconcile loop: what wakes a pass, and what the loop keeps from
// one pass to the next.
//
// The core of every operator is a level-triggered loop. Every pass
// reconciles from the current state as it is, never from the event
// that woke it, so missing one wake can never matter. Four things wake
// it. The Kubernetes watches wake the loop when an object this machine
// acts on changes, so a conductor's grant or a person's edit is acted
// on at once (watches.go names each watch and what wakes it). The facts
// watch wakes the loop when init publishes a change under
// /run/liken/facts, so a fresh fact like a time sync reaches status
// without waiting on a timer. The retry timer wakes the loop when the
// last pass left a step unfinished, or when a step asked to run again
// at a set time (retry.go). The ticker wakes the loop on a fixed
// cadence. It renews the heartbeat lease, and it catches the changes
// that no watch reports (main.go names them at the ticker).
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

	// ticks is the ticker's channel (main).
	ticks <-chan time.Time

	// watchFactsTree opens the facts watch. main passes
	// machine.WatchFactsTree, and a test passes a watch it controls.
	watchFactsTree func(ctx context.Context) (*factsWatch, error)

	retry retrySchedule
}

// run runs passes until ctx ends, starting from current, the Machine
// that main read or created.
func (l *loop) run(ctx context.Context, current *machine.Machine) {
	// The facts watch turns init's writes into wakes. inotify does not
	// recurse, so the watch reconciles its set with the tree before
	// every read (Sync, below). A watch that cannot start is not fatal:
	// the tree may not exist yet this early, so the operator logs the
	// error, runs on the ticker alone, and retries the watch on a later
	// ticker pass. The cost of a missing watch is latency, never
	// correctness.
	factsWatch := l.watchFacts(ctx)

	var retry *time.Timer
	for {
		// Sync before the read closes the window between a new subtree
		// and the watch on it: a directory that init created since the
		// last pass gets a watch now, before this pass reads the tree.
		//
		// A watch whose channel closed has a closed descriptor, and a
		// Sync on it would add watches to a descriptor number the
		// process may have given to something else since. So a closed
		// channel drops the watch here, and the select below never sees
		// it.
		if factsWatch != nil && watchStopped(factsWatch) {
			fmt.Fprintln(os.Stderr, "the facts watch stopped; watching again on the next tick")
			factsWatch = nil
		}
		if factsWatch != nil {
			if err := factsWatch.sync(); err != nil {
				fmt.Fprintf(os.Stderr, "syncing the facts watch: %v\n", err)
			}
		}
		// Each pass starts from the newest copy of this machine's
		// object. Status writes change resourceVersion, and
		// reconciling against a stale copy would make every status
		// update a conflict. A read that fails keeps the copy the last
		// pass had; the publish conflict retry handles a stale one.
		//
		// A Machine that is gone gets no heartbeat. The lease names the
		// Machine as its owner, so the garbage collector deletes the
		// lease with it, and a renewal from the last copy would create
		// the lease again, owned by a Machine that does not exist, for
		// the collector to delete again.
		out := &passOutcome{}
		renewing := l.heartbeat
		if fresh, err := l.objects.observedBy(out).machine(l.name); err == nil {
			current = fresh
		} else if errors.Is(err, apiclient.ErrNotFound) {
			renewing = nil
		}
		started := time.Now()
		err := reconcile(l.objects, current, l.clusterName, l.fetcher, renewing, l.layer, out)
		l.operator.ObserveReconcile(machineKind, time.Since(started), err)

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

		select {
		case <-ctx.Done():
			if retry != nil {
				retry.Stop()
			}
			return
		case <-l.wakes:
		case <-retryC:
		case _, ok := <-factsWake(factsWatch):
			// A closed channel means the watch died, and init's writes
			// since then reached nobody. The pass after this select
			// reads the whole tree, and the next tick opens the watch
			// again. The tick sets the pace of the reopens, so a watch
			// that dies the moment it opens costs one pass every ten
			// seconds, not a pass after every death.
			if !ok {
				fmt.Fprintln(os.Stderr, "the facts watch stopped; watching again on the next tick")
				factsWatch = nil
			}
		case <-l.ticks:
			// A watch that failed to start, or died, gets another try
			// here. Its error was logged when it first failed, so a
			// tree that stays missing logs nothing on each tick.
			if factsWatch == nil {
				if w, err := l.watchFactsTree(ctx); err == nil {
					factsWatch = w
				}
			}
		}
	}
}

// factsWatch is the part of the facts watch the loop uses: the wake
// channel and the Sync of a *machine.TreeWatch.
type factsWatch struct {
	wake <-chan struct{}
	sync func() error
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

// watchFacts starts the facts watch, or logs why it cannot and
// answers nil.
func (l *loop) watchFacts(ctx context.Context) *factsWatch {
	w, err := l.watchFactsTree(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "watching the facts tree: %v\n", err)
		return nil
	}
	return w
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

// factsWake returns the facts watch's wake channel, or a nil channel
// when there is no watch. A receive on a nil channel blocks forever, so
// the select arm simply never fires while the watch is down, and the
// ticker drives the passes on its own.
func factsWake(w *factsWatch) <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.wake
}
