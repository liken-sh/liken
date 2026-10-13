package main

// The supervisor of the runners. On each change it starts a runner for
// each Reservation of this operator that is Ready, and stops the runner
// of each one that is not. A reservation leaves Ready when it ends:
// observatory-operator's StopWork step stops the devices, and the runner
// stops with them. Then the supervisor ends the claims of every target
// whose status.reservation names a reservation that is not Ready: it
// clears status.reservation and status.exposure, and does not count the
// frame in flight. If the driver wrote that frame anyway, the promotion
// finds it in the outbox, and the catalog counts it. It also clears
// each status.failed that names a reservation that is not Ready.
//
// A runner that the operator's own stop ends keeps its claim and its
// status.exposure, so the next leader finishes the frame in flight.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// running is one runner and the way to stop it.
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
				// A refused write changes nothing that a watch delivers,
				// so a timer brings the next try.
				fmt.Fprintf(o.logs, "%s: releasing the targets of ended reservations: %v\n", managedBy, err)
				wait = retryWait
			}
		}
		if !sleep(ctx, wait, wake) {
			return
		}
	}
}

// superviseOnce starts and stops the runners for the reservations as
// they are now, and then releases the targets of the reservations that
// ended.
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

// releaseEnded ends the claim of each target whose status.reservation
// names a reservation that is not Ready, and clears each status.failed
// that names one. A failure holds only for the reservation it names, so
// a later reservation of the same name claims the target again.
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
		held, failed := t.Status.Reservation, t.Status.Failed
		claimEnded := held != "" && !ready[held]
		failureEnded := failed != nil && !ready[failed.Reservation]
		if !claimEnded && !failureEnded {
			continue
		}
		if _, err := o.writeTarget(t, func(fresh *astrophotography.Target, change *statusChange) bool {
			if failureEnded && fresh.Status.Failed != nil && *fresh.Status.Failed == *failed {
				fresh.Status.Failed = nil
			}
			if !claimEnded || fresh.Status.Reservation != held {
				return true
			}
			fresh.Status.Reservation, fresh.Status.Exposure = "", nil
			change.setImaging(fresh, false, astrophotography.ReasonWaiting,
				fmt.Sprintf("Reservation %s ended. The target waits for the next reservation.", held))
			return true
		}); err != nil {
			return fmt.Errorf("target %s: %w", targetName(t), err)
		}
	}
	return nil
}
