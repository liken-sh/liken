package main

// The Deployment's relay of an ask for a Player's screen. The node
// workload writes what it heard on the bus in the Television's
// status.screenAsk, and the Deployment asks the Player's screen once
// for each new ask, on the power topic of the Player's Receiver
// session. cecnode_screen_test.go tests what the node workload writes.

import (
	"sync"
	"testing"
	"time"
)

// screenCalls records each ask the relay makes.
type screenCalls struct {
	mutex sync.Mutex
	asks  []string
}

func (s *screenCalls) askScreen(player, screen, trigger string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.asks = append(s.asks, player+" "+screen+": "+trigger)
}

func (s *screenCalls) all() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.asks...)
}

// askedOf is the lounge Television with a screen ask in its status.
func askedOf(screen, at string) Television {
	television := lounge("")
	television.Status.Session = &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver"}
	television.Status.ScreenAsk = &TelevisionScreenAsk{At: at, Player: "media/den", Screen: screen, Cause: `"TV" (logical 0, 0.0.0.0) broadcast Set Stream Path 1.3.0.0`}
	return television
}

// An ask the Deployment finds when it starts is older than the start,
// and a person may have done anything since, so it relays nothing for
// it. Each new ask after that goes out once.
func TestTheDeploymentRelaysEachNewScreenAskOnce(t *testing.T) {
	t.Parallel()
	api := startCECAPI(t)
	api.putBus(scannedBus("den", tvDevice))
	api.putTelevision(askedOf(screenWake, "2026-09-29T20:00:00.000Z"))
	calls := &screenCalls{}
	controller := newCECBusController(api.client)
	controller.screens = calls
	mustSucceed(t, controller.pass())

	api.putTelevision(askedOf(screenWake, "2026-09-29T21:00:00.000Z"))
	mustSucceed(t, controller.pass())
	mustSucceed(t, controller.pass())
	api.putTelevision(askedOf(screenSleep, "2026-09-29T22:00:00.000Z"))
	mustSucceed(t, controller.pass())

	mustDeepEqual(t, calls.all(), []string{
		`media/den Wake: Television lounge: "TV" (logical 0, 0.0.0.0) broadcast Set Stream Path 1.3.0.0`,
		`media/den Sleep: Television lounge: "TV" (logical 0, 0.0.0.0) broadcast Set Stream Path 1.3.0.0`,
	})
}

// The session of the Player the ask names publishes it on its power
// topic, which the Player's screen client reads.
func TestTheSessionAsksItsScreenOnThePowerTopic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		screen  string
		payload string
	}{
		{screenWake, `{"action":"wake"}`},
		{screenSleep, `{"action":"sleep"}`},
	}
	for _, c := range cases {
		t.Run(c.screen, func(t *testing.T) {
			t.Parallel()
			h := newSessionHarness(t)
			h.powerTopic = testPowerTopic
			started := h.beginIdle(t, "GAME")
			broker := h.brokers.waitForSession(t)
			broker.waitForTopic(t, ownerTopic(testVolumeTopic))
			sessions := newTelevisionSessions(nil)
			t.Cleanup(sessions.stop)
			sessions.attach("theater", started)

			sessions.askScreen("theater", c.screen, "Television lounge: the TV asked")

			published := broker.waitForTopic(t, testPowerTopic)
			mustMatch(t, string(published.payload), c.payload)
			mustDeepEqual(t, waitForLines(t, h.log, "the TV asked", 1), []string{
				"Receiver theater: Television lounge: the TV asked; published " + c.payload + " to " + testPowerTopic,
			})
		})
	}
}

// A session that ended asks nothing, and the Deployment says why.
func TestAScreenAskWithNoSessionAsksNothing(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	h.powerTopic = testPowerTopic
	started := h.beginIdle(t, "GAME")
	broker := h.brokers.waitForSession(t)
	broker.waitForTopic(t, ownerTopic(testVolumeTopic))
	sessions := newTelevisionSessions(nil)
	t.Cleanup(sessions.stop)
	sessions.attach("theater", started)
	sessions.detach("theater", started)

	sessions.askScreen("theater", screenWake, "Television lounge: the TV asked")

	broker.refuseTopic(t, testPowerTopic, 100*time.Millisecond)
}
