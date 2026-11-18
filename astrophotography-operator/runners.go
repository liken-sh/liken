package main

// The supervisor runs one runner for each Ready Reservation held by
// this operator, and stops the runner when the Reservation leaves
// Ready. The runner does not stop the devices, because
// observatory-operator stops them in the Reservation's StopWork step.
//
// When a Reservation ends, the supervisor releases its Targets and does
// not count the frame that was in flight. If the driver wrote that
// frame anyway, promotion finds it in the outbox and the catalog counts
// it.
//
// When the operator itself stops, the Targets stay claimed and keep
// status.exposure, so the next leader can finish the frame in flight.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type running struct {
	stop context.CancelFunc
	done chan struct{}
}

// superviseRunners runs until ctx ends, and returns after every runner
// it started has returned.
func (o *operator) superviseRunners(ctx context.Context) {
	runners := map[string]*running{}
	defer func() {
		for _, r := range runners {
			r.stop()
			<-r.done
		}
	}()
	for {
		wake := o.changed.wait()
		wait := forever
		if o.stores.synced() {
			started := time.Now()
			err := o.superviseOnce(ctx, runners)
			o.metrics.observePass(observatory.ReservationKind.Name, time.Since(started), err != nil)
			if err != nil {
				// A failed write produces no watch event, so only a timer
				// retries it.
				fmt.Fprintf(o.logs, "%s: releasing the Targets of ended Reservations: %v\n", managedBy, err)
				wait = retryWait
			}
		}
		if !sleep(ctx, wait, wake) {
			return
		}
	}
}

func (o *operator) superviseOnce(ctx context.Context, runners map[string]*running) error {
	view := o.stores.collections[observatory.ReservationKind.Name].View()
	ready := map[string]*observatory.Reservation{}
	for _, res := range informer.CachedList[observatory.Reservation](view) {
		if isReady(&res) {
			ready[res.Metadata.UID] = &res
		}
	}
	for uid, r := range runners {
		if ready[uid] == nil {
			r.stop()
			<-r.done
			delete(runners, uid)
		}
	}
	for uid, res := range ready {
		if runners[uid] != nil {
			continue
		}
		runCtx, stop := context.WithCancel(ctx)
		r := &running{stop: stop, done: make(chan struct{})}
		runners[uid] = r
		go func() {
			defer close(r.done)
			newRunner(o, res).run(runCtx)
		}()
	}
	return o.releaseEnded()
}

// releaseEnded clears status.reservation and status.exposure on each
// Target claimed by a Reservation that is not Ready. It also clears
// each status.failed that names such a Reservation, because a failure
// keeps only that Reservation off the Target, and a later Reservation
// with the same name may claim it.
func (o *operator) releaseEnded() error {
	targets, err := o.listTargets("")
	if err != nil {
		return err
	}
	readyByNamespace := map[string]map[string]bool{}
	for i := range targets {
		t := &targets[i]
		ready, ok := readyByNamespace[t.Metadata.Namespace]
		if !ok {
			ready = o.readyIn(t.Metadata.Namespace)
			readyByNamespace[t.Metadata.Namespace] = ready
		}
		claimedBy, failed := t.Status.Reservation, t.Status.Failed
		claimEnded := claimedBy != "" && !ready[claimedBy]
		failureEnded := failed != nil && !ready[failed.Reservation]
		if !claimEnded && !failureEnded {
			continue
		}
		if _, err := o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
			if failureEnded && fresh.Status.Failed != nil && *fresh.Status.Failed == *failed {
				fresh.Status.Failed = nil
			}
			if !claimEnded || fresh.Status.Reservation != claimedBy {
				return true
			}
			fresh.Status.Reservation, fresh.Status.Exposure = "", nil
			change.setImaging(fresh, false, astrophotography.ReasonWaiting,
				fmt.Sprintf("Reservation %s ended, and the target waits for the next reservation", claimedBy))
			return true
		}); err != nil {
			return fmt.Errorf("writing the status of Target %s: %w", targetName(t), err)
		}
	}
	return nil
}
