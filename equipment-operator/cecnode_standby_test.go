package main

// cecnode_wake_test.go has the session fixtures that these tests use.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// pressedOff returns woke as the Deployment writes it after a press of
// the remote's power button turns the room off.
func pressedOff(woke *TelevisionSession) *TelevisionSession {
	off := *woke
	off.Awake = false
	off.StandbyAt = time.Now().UTC().Format(wakeTimeLayout)
	return &off
}

// waitForStandbyResult waits until the node workload has written the
// final StandbyApplied condition of the session's standby.
func waitForStandbyResult(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.StandbyAt == session.StandbyAt && conditionOf(television.Status.Conditions, conditionStandbyApplied).Reason != reasonEnteringStandby
	})
}

// awakeAtStart starts the node workload for node-1 on a bus in Control,
// with tv on the wire and session already in the Television.
func awakeAtStart(t *testing.T, tv cectest.Peer, session *TelevisionSession) (*cecAPI, *cectest.Bus, *logBuffer) {
	t.Helper()
	wire := busWithTV(tv)
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(waking(session))
	log := loggedNode(t, api, device)
	api.scanned(t, "node-1")
	return api, wire, log
}

// waitLongerThanAStandby runs a reconcile pass and waits longer than a
// standby takes, so a test can assert that a command was never sent.
func waitLongerThanAStandby(api *cecAPI) {
	api.nudge()
	time.Sleep(2 * cecWakeGuard)
}

func TestAPowerPressPutsTheTVInStandbyOnce(t *testing.T) {
	cases := []struct {
		name     string
		tv       cec.PowerStatus
		standbys int
		outcome  string
	}{
		{"a TV that is on", cec.PowerOn, 1,
			"the adapter on node-1 sent Standby to the TV once; the TV reported Standby after <time>"},
		{"a TV already in standby", cec.PowerStandby, 0,
			"the TV already reported Standby, so the adapter on node-1 sent no command"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				woke := wokeAt(time.Now().Add(-time.Minute))
				api, wire, log := awakeAtStart(t, televisionTV(c.tv), woke)
				off := pressedOff(woke)

				api.putTelevision(waking(off))

				television := waitForStandbyResult(t, api, off)
				applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
				mustMatch(t, applied.Status, ConditionTrue)
				mustMatch(t, applied.Reason, reasonConfirmed)
				waitLongerThanAStandby(api)
				mustMatch(t, sentOf(wire, cec.OpStandby), c.standbys)
				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
				mustCommandTheTVAlone(t, wire)
				peer, _ := wire.Peer(0)
				mustMatch(t, peer.Power, cec.PowerStandby)
				mustDeepEqual(t, timeless(linesWith(log, "turned Player")), []string{
					"Television lounge: a powerAsk turned Player media/den's room off at " + off.StandbyAt + "; " + c.outcome,
				})
			})
		})
	}
}

// A standbyAt that is already in the status when the node workload
// starts may be a press handled before a restart, so the node workload
// sends nothing and logs that once.
func TestAStandbyFoundAtStartSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		off := pressedOff(wokeAt(time.Now().Add(-time.Minute)))

		api, wire, log := awakeAtStart(t, televisionTV(cec.PowerOn), off)
		waitLongerThanAStandby(api)

		mustMatch(t, sentOf(wire, cec.OpStandby), 0)
		television, _ := api.television("lounge")
		mustMatch(t, television.Status.StandbyAt, "")
		mustDeepEqual(t, linesWith(log, "turned Player"), []string{
			"Television lounge: a powerAsk turned Player media/den's room off at " + off.StandbyAt +
				"; the standby was in the status when the node workload started, so the adapter on node-1 sends nothing for it",
		})
	})
}

// A node workload that starts after a standby finds the standby in
// status.standbyAt and sends nothing more.
func TestAStandbyRunsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		woke := wokeAt(time.Now().Add(-time.Minute))
		api := startCECAPI(t)
		adapter, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(waking(woke))
		done := startNode(t, api, "node-1", device)
		api.scanned(t, "node-1")
		off := pressedOff(woke)
		api.putTelevision(waking(off))
		waitForStandbyResult(t, api, off)
		wire.Add(televisionTV(cec.PowerOn))

		adapter.Unplug()
		<-done
		_, again := usbAdapter(wire)
		startNode(t, api, "node-1", again)
		api.scanned(t, "node-1")
		waitLongerThanAStandby(api)

		mustMatch(t, sentOf(wire, cec.OpStandby), 1)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerOn)
	})
}

// Only a powerAsk that turns the room off turns the TV off. A session that goes to sleep or
// is removed sends the TV nothing.
func TestASleepOrARemovedSessionSendsTheTVNothing(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, api *cecAPI, woke *TelevisionSession)
	}{
		{"a session that sleeps", func(t *testing.T, api *cecAPI, woke *TelevisionSession) {
			asleep := *woke
			asleep.Awake = false
			api.putTelevision(waking(&asleep))
		}},
		{"a session that is removed", func(t *testing.T, api *cecAPI, _ *TelevisionSession) {
			mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", nil))
		}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				woke := wokeAt(time.Now().Add(-time.Minute))
				api, wire, _ := awakeAtStart(t, televisionTV(cec.PowerOn), woke)

				c.change(t, api, woke)
				waitLongerThanAStandby(api)

				mustMatch(t, sentOf(wire, cec.OpStandby), 0)
				peer, _ := wire.Peer(0)
				mustMatch(t, peer.Power, cec.PowerOn)
				television, _ := api.television("lounge")
				mustMatch(t, television.Status.StandbyAt, "")
			})
		})
	}
}

// A press that turns the room on during a standby stops the standby,
// and the wake runs.
func TestAWakeStopsAStandby(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stubborn := stubbornTV()
		stubborn.Power = cec.PowerOn
		woke := wokeAt(time.Now().Add(-time.Minute))
		api, wire, _ := awakeAtStart(t, stubborn, woke)
		off := pressedOff(woke)
		api.putTelevision(waking(off))
		api.waitUntil(t, "the first Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })

		again := wokeNow()
		api.putTelevision(waking(again))

		television := waitForStandbyResult(t, api, off)
		applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
		mustMatch(t, applied.Reason, reasonStopped)
		mustMatch(t, timeless([]string{applied.Message})[0],
			"The adapter on node-1 sent Standby to the TV once, and the standby stopped after <time>, before the TV reported Standby")
		api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.WokeAt == again.WokeAt })
	})
}

// A new generation of spec.power that arrives during a standby stops it
// for good.
func TestAPendingPowerComesBeforeAStandby(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stubborn := stubbornTV()
		stubborn.Power = cec.PowerOn
		woke := wokeAt(time.Now().Add(-time.Minute))
		api, wire, log := awakeAtStart(t, stubborn, woke)
		off := pressedOff(woke)
		api.putTelevision(waking(off))
		api.waitUntil(t, "the first Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })

		api.putTelevision(lounge(TelevisionOn))

		television := waitForStandbyResult(t, api, off)
		applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
		mustMatch(t, applied.Reason, reasonSuperseded)
		mustMatch(t, applied.Message, "spec.power at generation 2 is On, so the standby stopped, and the adapter on node-1 sends nothing more for it")
		mustDeepEqual(t, linesWith(log, "turned Player"), []string{
			"Television lounge: a powerAsk turned Player media/den's room off at " + off.StandbyAt + "; " + applied.Message,
		})
	})
}

// A standby that waits longer than cecWakeFresh sends nothing. Here it
// waits behind a spec.power whose status write the API server refuses.
func TestAStandbyThatWaitsTooLongSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		woke := wokeAt(time.Now().Add(-time.Minute))
		api, wire, log := awakeAtStart(t, televisionTV(cec.PowerOn), woke)
		api.refusePowerWrites(true)
		off := pressedOff(woke)
		television := waking(off)
		television.Spec.Power = TelevisionOn

		api.putTelevision(television)
		time.Sleep(cecWakeFresh + time.Second)
		api.nudge()

		recorded := waitForStandbyResult(t, api, off)
		applied := conditionOf(recorded.Status.Conditions, conditionStandbyApplied)
		mustMatch(t, applied.Reason, reasonTooLate)
		mustMatch(t, sentOf(wire, cec.OpStandby), 0)
		mustMatch(t, len(linesWith(log, "so the standby waits for it")), 1)
	})
}
