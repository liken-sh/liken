package main

// The Deployment's pass over Televisions against the fake API server:
// discovery, adoption, and the status it derives from the CECBus, the
// Displays, and the Receivers.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// scannedBus is a bus in Control whose adapter on node-1 reported a
// scan just now that found the given devices.
func scannedBus(name string, devices ...CECDevice) CECBus {
	bus := controlBus(name, CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"})
	entry := scannedEntry("node-1", 4, devices...)
	entry.ReportedAt = timestamp(time.Now())
	bus.Status.Adapters = []CECAdapterStatus{entry}
	return bus
}

// passes runs the Deployment's pass as many times as asked.
func passes(t *testing.T, api *cecAPI, count int) {
	t.Helper()
	controller := newCECBusController(api.client)
	for range count {
		mustSucceed(t, controller.pass())
	}
}

func TestTheDeploymentDiscoversTheTVOfABusInControl(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice, receiverDevice))

	passes(t, api, 2)

	television, held := api.television("den")
	if !held {
		t.Fatal("no Television for the bus's TV")
	}
	mustMatch(t, television.Metadata.Labels[discoveredLabel], "cec")
	mustDeepEqual(t, television.Spec, TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}})
	mustMatch(t, television.Status.Power, "Standby")
	mustMatch(t, conditionOf(television.Status.Conditions, conditionReachable).Status, ConditionTrue)
}

// Discovery creates with a create, not an apply, so an object that
// already has the bus's name is left as it is.
func TestDiscoveryCreatesAndLeavesAnExistingName(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))

	passes(t, api, 3)

	mustDeepEqual(t, api.created, []string{"den"})
	mustSucceed(t, CreateDiscoveredTelevision(api.client, "den"))
	mustDeepEqual(t, api.created, []string{"den"})
}

// A person adopts the discovered Television by applying their own spec
// to it under the bus's name. It keeps the discovered label and stays,
// because no other Television names the bus.
func TestAPersonAdoptsTheDiscoveredTelevisionUnderItsName(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	passes(t, api, 1)

	api.putTelevision(Television{Metadata: ObjectMeta{Name: "den"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}, Power: TelevisionOn}})
	passes(t, api, 2)

	television, held := api.television("den")
	if !held || television.Spec.Power != TelevisionOn {
		t.Fatalf("the adopted Television is %+v (held %v)", television, held)
	}
	mustMatch(t, television.Metadata.Labels[discoveredLabel], "cec")
	mustDeepEqual(t, api.deletedTelevisionNames(), []string(nil))
	mustMatch(t, conditionOf(television.Status.Conditions, conditionInCharge).Status, ConditionTrue)
}

func TestAPersonsTelevisionTakesOverFromTheDiscoveredOne(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	passes(t, api, 1)

	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	passes(t, api, 1)

	if !slices.Contains(api.deletedTelevisionNames(), "den") {
		t.Error("the discovered Television is still there")
	}
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.Power, "Standby")
}

func TestTheDeploymentWritesWhatTheBusFound(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice, receiverDevice))
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putReceiver(wiredReceiver("den", "node-1", "acm-0001-receiver"))
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})

	passes(t, api, 1)

	television, _ := api.television("lounge")
	mustDeepEqual(t, television.Status.CEC, &TelevisionCECStatus{PhysicalAddress: "0.0.0.0", LogicalAddress: 0, OSDName: "TV"})
	mustDeepEqual(t, television.Status.Displays, []TelevisionDisplay{
		{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}},
	})
}

// A pass that finds nothing changed writes nothing, so the Deployment's
// backstop tick costs the API server no write.
func TestAnUnchangedTelevisionIsNotWrittenAgain(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	passes(t, api, 1)
	before, _ := api.writes()

	passes(t, api, 2)

	after, _ := api.writes()
	mustMatch(t, after, before)
}

// A TV that stops answering loses its power, because a power the TV
// did not state now would be a guess.
func TestATVThatStopsAnsweringLosesItsPower(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	passes(t, api, 1)
	silent := tvDevice
	silent.Power = ""

	api.putBus(scannedBus("den", silent))
	passes(t, api, 1)

	television, _ := api.television("lounge")
	mustMatch(t, television.Status.Power, "")
	mustMatch(t, conditionOf(television.Status.Conditions, conditionReachable).Reason, reasonNoPower)
}

// The Deployment's loop wakes on a new Television and writes its
// status with no wait for the backstop.
func TestTheDeploymentLoopFollowsTheTelevisions(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		newCECBusController(api.client).run(ctx, newMetrics("test"))
	}()
	t.Cleanup(func() { cancel(); <-done })
	api.waitForTelevision(t, "den", func(Television) bool { return true })

	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})

	api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.Power == "Standby" })
}

// A read that fails skips the Television pass, because a status
// derived from a partial read would remove facts that are still true.
// A write the API server refuses is logged and left for the next pass.
func TestTheTelevisionPassSkipsAFailedReadAndSurvivesARefusedWrite(t *testing.T) {
	buses := CECBusList{Items: []CECBus{*busWith(CECControl, []string{"node-1"}, scannedEntry("node-1", 4, tvDevice))}}
	televisions := TelevisionList{Items: []Television{*tvOn("den")}}
	everything := map[string]any{
		"GET " + cecBusesPath:    buses,
		"GET " + televisionsPath: televisions,
		"GET " + displaysPath:    DisplayList{},
		"GET " + receiversPath:   ReceiverList{},
	}
	without := func(path string) map[string]any {
		answers := map[string]any{}
		for key, answer := range everything {
			if key != "GET "+path {
				answers[key] = answer
			}
		}
		return answers
	}
	cases := []struct {
		name     string
		answers  map[string]any
		statuses map[string]int
		writes   bool
	}{
		{"no Television definition", without(televisionsPath), nil, false},
		{"the Televisions refused", everything, map[string]int{"GET " + televisionsPath: http.StatusInternalServerError}, false},
		{"no Receiver collection", without(receiversPath), nil, false},
		{"the Displays refused", everything, map[string]int{"GET " + displaysPath: http.StatusInternalServerError}, false},
		{"no Display collection", without(displaysPath), nil, true},
		{"every write refused", everything, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &cannedAPI{answers: c.answers, statuses: c.statuses}
			controller := newCECBusController(testAPIClient(t, api.handler()))
			controller.now = func() time.Time { return derivedAt }

			mustSucceed(t, controller.pass())

			wrote := slices.ContainsFunc(api.requests, func(request recordedRequest) bool {
				return request.Method == http.MethodPatch && strings.HasPrefix(request.Path, televisionsPath)
			})
			mustMatch(t, wrote, c.writes)
			wroteBus := slices.ContainsFunc(api.requests, func(request recordedRequest) bool {
				return request.Method == http.MethodPatch && strings.HasPrefix(request.Path, cecBusesPath)
			})
			mustMatch(t, wroteBus, true)
		})
	}
}

func TestDeleteTelevisionSettlesOnGoneAndReportsARefusal(t *testing.T) {
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
			api := &cannedAPI{statuses: map[string]int{"DELETE " + televisionPath("den"): c.status}}

			err := DeleteTelevision(testAPIClient(t, api.handler()), "den")

			if (err != nil) != c.wantErr {
				t.Errorf("got %v, want an error: %v", err, c.wantErr)
			}
		})
	}
}

// loggedPasses runs the Deployment's pass as many times as asked, with
// its log in a buffer.
func loggedPasses(t *testing.T, api *cecAPI, count int) *logBuffer {
	t.Helper()
	controller := newCECBusController(api.client)
	log := &logBuffer{}
	controller.log = log
	for range count {
		mustSucceed(t, controller.pass())
	}
	return log
}

// Discovery writes one line when it creates a Television and one when
// it deletes it, and later passes write none.
func TestDiscoveryLogsEachTelevisionItCreatesOrDeletes(t *testing.T) {
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	created := loggedPasses(t, api, 3)

	api.putTelevision(Television{Metadata: ObjectMeta{Name: "lounge"}, Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: "den"}}})
	deleted := loggedPasses(t, api, 3)

	mustDeepEqual(t, created.lines(), []string{
		`CECBus den reports a TV at 0.0.0.0 named "TV", and no Television names the bus; created Television den`,
	})
	mustDeepEqual(t, deleted.lines(), []string{
		"deleted the discovered Television den: Television lounge names CECBus den",
	})
}
