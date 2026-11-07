package main

// The Deployment's CECBus loop follows the pods of the node workload:
// an entry is current while its machine's pod is Ready, and the pod
// watch wakes the loop when that changes, so no clock runs a pass.

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/events"
)

// runEventedLoop runs the Deployment's CECBus loop with a recorder
// until the test ends, and answers the controller once every store
// holds its first read.
func runEventedLoop(t *testing.T, api *cecAPI) *cecBusController {
	t.Helper()
	controller := newCECBusController(api.client, testNamespace)
	controller.recorder = testRecorder(t, api.client)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		controller.run(ctx, testMetrics(t))
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	awaitStores(t, controller.buses, controller.televisions, controller.displays, controller.receivers, controller.pods)
	return controller
}

// waitForJoined waits until the den bus's Joined condition has a reason
// and a message that starts with the given text.
func waitForJoined(t *testing.T, api *cecAPI, reason, message string) {
	t.Helper()
	api.waitUntil(t, "Joined to be "+reason+": "+message, func() bool {
		bus, _ := api.bus("den")
		joined := conditionOf(bus.Status.Conditions, conditionJoined)
		return joined.Reason == reason && strings.HasPrefix(joined.Message, message)
	})
}

// The pod's Ready condition decides whether the entry is current. A
// pod that stops being Ready, or is deleted, makes the entry stale at
// once, and a pod that is Ready again makes it current. Each change of
// a condition's reason posts one Event, and a new message alone posts
// none.
func TestTheLoopFollowsTheNodeWorkloadsPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putPod("node-1", true)
		api.putBus(*busWith(CECControl, []string{"node-1"}, writtenNow(scannedEntry("node-1", 4, tvDevice))))
		runEventedLoop(t, api)
		waitForJoined(t, api, conditionJoined, "")
		time.Sleep(time.Second)

		api.putPod("node-1", false)
		waitForJoined(t, api, reasonStale,
			"the equipment-operator-cec pod on machine node-1 stopped being Ready at 2000-01-01T00:00:01Z, after its entry was last written at 2000-01-01T00:00:00Z, so the entry may not describe the adapter")
		bus, _ := api.bus("den")
		mustDeepEqual(t, bus.Status.Devices, []CECDevice(nil))

		api.removePod("node-1")
		waitForJoined(t, api, reasonStale, "no equipment-operator-cec pod runs on machine node-1")

		api.putPod("node-1", true)
		waitForJoined(t, api, conditionJoined, "")

		var transitions []posted
		for _, event := range postedAbout(api.events, "CECBus", "den") {
			if event.reason != reasonTelevisionCreated {
				transitions = append(transitions, event)
			}
		}
		mustDeepEqual(t, transitions, []posted{
			{events.TypeNormal, conditionAddressKnown, 2},
			{events.TypeNormal, conditionJoined, 2},
			{events.TypeNormal, reasonOneAdapter, 2},
			{events.TypeNormal, conditionScanned, 2},
			{events.TypeWarning, reasonStale, 4},
		})
	})
}

// A Stopped entry is the node workload's own last word, so a Ready pod
// on the machine does not make it current.
func TestAStoppedEntryWinsOverAReadyPod(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putPod("node-1", true)
		api.putBus(*busWith(CECControl, []string{"node-1"}, stopped("node-1")))

		runEventedLoop(t, api)

		waitForJoined(t, api, reasonStopped, "")
	})
}

// A loop with nothing to do sends the API server nothing, however long
// it runs: no clock reads the buses again.
func TestAnIdleLoopSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putBus(*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice)))
		runEventedLoop(t, api)
		waitForJoined(t, api, conditionJoined, "")
		synctest.Wait()
		api.mutex.Lock()
		version, reads := api.version, len(api.reads)
		counted := 0
		for _, count := range api.reads {
			counted += count
		}
		api.mutex.Unlock()

		time.Sleep(10 * time.Minute)

		api.mutex.Lock()
		defer api.mutex.Unlock()
		mustMatch(t, api.version, version)
		mustMatch(t, len(api.reads), reads)
		total := 0
		for _, count := range api.reads {
			total += count
		}
		mustMatch(t, total, counted)
	})
}

// A status write the API server refuses is tried again after
// cecBusRetry, with no event to wake the loop.
func TestARefusedCECBusStatusWriteIsTriedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putPod("node-1", true)
		api.putBus(*busWith(CECControl, []string{"node-1"}, writtenNow(scannedEntry("node-1", 4, tvDevice))))
		runEventedLoop(t, api)
		waitForJoined(t, api, conditionJoined, "")
		time.Sleep(time.Second)
		api.refuse(true)
		api.putPod("node-1", false)
		synctest.Wait()
		bus, _ := api.bus("den")
		mustMatch(t, conditionOf(bus.Status.Conditions, conditionJoined).Reason, conditionJoined)

		api.refuse(false)
		time.Sleep(cecBusRetry)

		waitForJoined(t, api, reasonStale, "")
	})
}

// A node workload writes its first entry before its pod is Ready. The
// pod's Ready condition last changed before that write, so the entry is
// current and posts no Stale. A pod that is still not Ready
// cecStartupGrace after the write, such as one whose loop hangs at its
// start, makes the entry stale, with no event to wake the loop.
func TestAStartingPodsEntryIsCurrentUntilTheGraceEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putPod("node-1", false)
		time.Sleep(time.Second)
		api.putBus(*busWith(CECControl, []string{"node-1"}, writtenNow(scannedEntry("node-1", 4, tvDevice))))
		runEventedLoop(t, api)
		waitForJoined(t, api, conditionJoined, "")
		time.Sleep(cecStartupGrace / 2)
		synctest.Wait()
		mustNotPost(t, api, reasonStale)

		time.Sleep(cecStartupGrace / 2)

		waitForJoined(t, api, reasonStale,
			"the equipment-operator-cec pod on machine node-1 has not been Ready in the 1m0s since its entry was written at 2000-01-01T00:00:01Z, so the entry may not describe the adapter")
	})
}

// A starting pod that becomes Ready within the grace never makes its
// entry stale.
func TestAPodThatBecomesReadyInTheGracePostsNoStale(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		api.putPod("node-1", false)
		time.Sleep(time.Second)
		api.putBus(*busWith(CECControl, []string{"node-1"}, writtenNow(scannedEntry("node-1", 4, tvDevice))))
		runEventedLoop(t, api)
		waitForJoined(t, api, conditionJoined, "")

		api.putPod("node-1", true)
		time.Sleep(2 * cecStartupGrace)

		mustNotPost(t, api, reasonStale)
	})
}

// mustNotPost fails the test when the den bus received an Event with
// the reason.
func mustNotPost(t *testing.T, api *cecAPI, reason string) {
	t.Helper()
	for _, event := range postedAbout(api.events, "CECBus", "den") {
		if event.reason == reason {
			t.Errorf("the bus received %+v", event)
		}
	}
}
