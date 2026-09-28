package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// awaitWork answers false while a counted goroutine runs past the
// bound, and true once every one has stopped. A context with no group
// runs the work anyway.
func TestAwaitWorkAnswersWhetherTheWorkStopped(t *testing.T) {
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
}

// At a shutdown, the Receiver loop returns only after a settings write
// that is in flight has finished, because the Deployment releases its
// Lease after the loop returns.
func TestTheLoopWaitsForAWriteInFlightBeforeItReturns(t *testing.T) {
	noDiscovery(t)
	api := startFakeAPI(t)
	fake := startFakeDenon(t)
	brokers := startFakeBrokerServer(t)
	receiver := testReceiver("theater", fake.address())
	receiver.Spec.SettingsTopic = "liken/equipment/theater/settings"
	api.setReceivers(receiver)
	operator := newController(api.client, brokers.address(), testMetrics(t))
	operator.now = func() time.Time { return statusNow }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		operator.run(ctx)
	}()
	api.waitForStatus(t, connected)
	broker := brokers.waitForSession(t)
	waitForString(t, broker.subs)
	api.gateSettings()
	defer api.releaseSettings()
	broker.push(receiver.Spec.SettingsTopic, []byte(`{"setting":"tone.bass","value":3}`))
	select {
	case <-api.settings:
	case <-time.After(testTimeout):
		t.Fatal("the settings write never reached the API server")
	}

	cancel()

	select {
	case <-stopped:
		t.Fatal("the loop returned while a settings write was in flight")
	case <-time.After(300 * time.Millisecond):
	}
	api.releaseSettings()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("the loop did not return after the write finished")
	}
	mustMatch(t, operator.stopped, true)
}
