package main

// The Deployment's CECBus loop against the fake API server.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTheLoopWritesWhatItDerives(t *testing.T) {
	api := startCECAPI(t)
	bus := *busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice))
	api.putBus(bus)
	controller := newCECBusController(api.client)
	controller.now = func() time.Time { return derivedAt }

	mustSucceed(t, controller.pass())

	written, _ := api.bus("den")
	mustDeepEqual(t, written.Status.Devices, []CECDevice{tvDevice})
	mustMatch(t, conditionOf(written.Status.Conditions, conditionScanned).Status, ConditionTrue)
}

func TestASecondPassWritesNothing(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice)))
	controller := newCECBusController(api.client)
	controller.now = func() time.Time { return derivedAt }
	mustSucceed(t, controller.pass())
	api.mutex.Lock()
	before := api.version
	api.mutex.Unlock()

	mustSucceed(t, controller.pass())

	api.mutex.Lock()
	defer api.mutex.Unlock()
	mustMatch(t, api.version, before)
}

func TestTheLoopFollowsAnAdaptersReport(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(*busWith(CECControl, []string{"node-1"}))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		newCECBusController(api.client).run(ctx, testMetrics(t))
	}()
	t.Cleanup(func() { cancel(); <-stopped })

	mustSucceed(t, ApplyCECAdapterStatus(api.client, "den", "node-1", &CECAdapterStatus{Machine: "node-1", State: AdapterScanned, PhysicalAddress: "1.3.0.0", LogicalAddress: logical(4), Devices: []CECDevice{tvDevice}, ReportedAt: timestamp(time.Now())}))

	api.waitUntil(t, "the derived devices", func() bool {
		bus, _ := api.bus("den")
		return len(bus.Status.Devices) == 1 && conditionOf(bus.Status.Conditions, conditionJoined).Status == ConditionTrue
	})
}

// A cluster without the CECBus definition answers the list with an
// error, and the loop waits and lists again until its context ends.
func TestTheLoopWaitsForTheDefinition(t *testing.T) {
	was := cecBusRetry
	cecBusRetry = time.Millisecond
	t.Cleanup(func() { cecBusRetry = was })
	api := &cannedAPI{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	newCECBusController(testAPIClient(t, api.handler())).run(ctx, testMetrics(t))

	if len(api.requests) < 2 {
		t.Errorf("the loop listed %d times", len(api.requests))
	}
}

// A watch that ends is opened again from a fresh list, and each
// reopening is counted.
func TestTheCECBusWatchReopens(t *testing.T) {
	was := watchRetryPause
	watchRetryPause = time.Millisecond
	t.Cleanup(func() { watchRetryPause = was })
	api := &cannedAPI{answers: map[string]any{"GET " + cecBusesPath: CECBusList{Metadata: ListMeta{ResourceVersion: "7"}}}}
	client := testAPIClient(t, api.handler())
	ctx, cancel := context.WithCancel(context.Background())
	wake := make(chan struct{}, 1)
	restarts := make(chan struct{}, 64)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		watchCECBuses(ctx, client, "1", wake, func() {
			select {
			case restarts <- struct{}{}:
			default:
			}
		})
	}()

	select {
	case <-restarts:
	case <-time.After(testTimeout):
		t.Error("the watch never reopened")
	}
	cancel()
	<-stopped
}

func TestDeleteCECBusSettlesOnGoneAndReportsARefusal(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"deleted", http.StatusOK, false},
		{"already gone", http.StatusNotFound, false},
		{"refused", http.StatusForbidden, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &cannedAPI{statuses: map[string]int{"DELETE " + cecBusPath("node-1"): c.status}}

			err := DeleteCECBus(testAPIClient(t, api.handler()), "node-1")

			if (err != nil) != c.wantErr {
				t.Errorf("got %v, want an error: %v", err, c.wantErr)
			}
		})
	}
}

// A failed list is the pass's error, and a refused write is logged and
// left for the next pass.
func TestAPassReportsAFailedListAndSurvivesARefusedWrite(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]any
		wantErr bool
	}{
		{"no collection", map[string]any{}, true},
		{"a refused write", map[string]any{"GET " + cecBusesPath: CECBusList{Items: []CECBus{*busWith(CECListen, []string{"node-1"})}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &cannedAPI{answers: c.answers}

			err := newCECBusController(testAPIClient(t, api.handler())).pass()

			if (err != nil) != c.wantErr {
				t.Errorf("got %v, want an error: %v", err, c.wantErr)
			}
		})
	}
}

// A pod that dies writes nothing more, so the backstop tick is what
// finds its entry stale.
func TestTheBackstopFindsAnEntryGoneStale(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice)))
	controller := newCECBusController(api.client)
	controller.now = func() time.Time { return derivedAt }
	mustSucceed(t, controller.pass())

	controller.now = func() time.Time { return derivedAt.Add(2 * time.Minute) }
	mustSucceed(t, controller.pass())

	bus, _ := api.bus("den")
	mustMatch(t, conditionOf(bus.Status.Conditions, conditionJoined).Reason, reasonStale)
	mustDeepEqual(t, bus.Status.Devices, []CECDevice(nil))
}
