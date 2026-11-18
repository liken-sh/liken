package main

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// awaitWork returns false while a goroutine of the group runs, and true
// after it stops. goWork still runs work under a context with no group.
func TestAwaitWorkReturnsWhetherTheWorkStopped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var group sync.WaitGroup
		ctx := withWork(context.Background(), &group)
		release := make(chan struct{})
		goWork(ctx, func() { <-release })
		uncounted := make(chan struct{})
		goWork(context.Background(), func() { close(uncounted) })

		mustMatch(t, awaitWork(&group, 50*time.Millisecond), false)
		close(release)
		mustMatch(t, awaitWork(&group, testTimeout), true)
		select {
		case <-uncounted:
		case <-time.After(testTimeout):
			t.Fatal("work under a context with no group never ran")
		}
	})
}

// On shutdown, a status write that waits after a 429 response stops
// waiting, so the Receiver loop returns at once and the operator can
// release its Lease.
func TestAShutdownEndsTheWaitOfAWriteThatMetA429(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		api.setReceivers(testReceiver("theater", fake.address()))
		api.mutex.Lock()
		api.throttlingStatus = true
		api.mutex.Unlock()
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.listen = testNetwork.bind
		operator.networkDiscoveryOff = true
		operator.now = func() time.Time { return statusNow }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			operator.run(ctx)
		}()
		waitFor(t, func() bool {
			api.mutex.Lock()
			defer api.mutex.Unlock()
			return api.statusThrottles > 0
		})
		began := time.Now()

		cancel()

		select {
		case <-stopped:
		case <-time.After(workStopWait + testTimeout):
			t.Fatal("the loop did not return")
		}
		if took := time.Since(began); took > time.Second {
			t.Errorf("the loop returned after %s", took)
		}
		mustMatch(t, operator.stopped, true)
	})
}

// On shutdown, the Receiver loop returns only after a power write in
// flight has finished, because the operator releases its Lease when the
// loop returns.
func TestTheLoopWaitsForAWriteInFlightBeforeItReturns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startFakeAPI(t)
		fake := startFakeDenon(t)
		receiver := sessionReceiver(fake.address())
		api.setReceivers(receiver)
		operator := newController(api.client, testMetrics(t))
		operator.dial = testNetwork.dial
		operator.listen = testNetwork.bind
		operator.networkDiscoveryOff = true
		operator.now = func() time.Time { return statusNow }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			operator.run(ctx)
		}()
		api.waitForStatus(t, connected)
		api.gatePowers()
		defer api.releasePowers()
		api.setReceivers(askingPower(receiver, "toggle", askAt(1)))
		sendWake(operator.wake)
		select {
		case <-api.powers:
		case <-time.After(testTimeout):
			t.Fatal("the power write never reached the API server")
		}

		cancel()

		select {
		case <-stopped:
			t.Fatal("the loop returned while a power write was in flight")
		case <-time.After(300 * time.Millisecond):
		}
		api.releasePowers()
		select {
		case <-stopped:
		case <-time.After(testTimeout):
			t.Fatal("the loop did not return after the write finished")
		}
		mustMatch(t, operator.stopped, true)
	})
}
