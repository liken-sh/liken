package main

// The node workload's standby of a Television's session: a power press
// that turns the room off sends the TV Standby once, a TV already in
// standby gets nothing, a restart replays nothing, and a sleep or a
// lift of the session sends nothing. cecnode_wake_test.go holds the
// session fixtures.

import (
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// pressedOff is the session the Deployment writes when the remote's
// power button turns the room off: asleep, with the wake it held and
// the time of the press.
func pressedOff(woke *TelevisionSession) *TelevisionSession {
	off := *woke
	off.Awake = false
	off.StandbyAt = time.Now().UTC().Format(wakeTimeLayout)
	return &off
}

// stoodBy waits until the node workload recorded the result of a
// session's standby.
func stoodBy(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.StandbyAt == session.StandbyAt && conditionOf(television.Status.Conditions, conditionStandbyApplied).Reason != reasonEnteringStandby
	})
}

// awakeAtStart runs the node workload for node-1 on a bus in Control,
// with a TV and a session that woke the room before the node workload
// started. The node workload adopts that wake and sends nothing for
// it, as it does after a restart.
func awakeAtStart(t *testing.T, tv cectest.Peer, session *TelevisionSession) (*cecAPI, *cectest.Bus, *logBuffer) {
	t.Helper()
	wire := roomWithTV(tv)
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(waking(session))
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	return api, wire, log
}

// quiet waits for more than a whole standby would take, with a pass in
// between, so a test can assert what never went out.
func quiet(api *cecAPI) {
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			woke := wokeAt(time.Now().Add(-time.Minute))
			api, wire, log := awakeAtStart(t, televisionTV(c.tv), woke)
			off := pressedOff(woke)

			api.putTelevision(waking(off))

			television := stoodBy(t, api, off)
			applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
			mustMatch(t, applied.Status, ConditionTrue)
			mustMatch(t, applied.Reason, reasonConfirmed)
			quiet(api)
			mustMatch(t, sentOf(wire, cec.OpStandby), c.standbys)
			mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
			mustCommandTheTVAlone(t, wire)
			peer, _ := wire.Peer(0)
			mustMatch(t, peer.Power, cec.PowerStandby)
			mustDeepEqual(t, timeless(linesWith(log, "turned Player")), []string{
				"Television lounge: the remote's power button turned Player media/den's room off at " + off.StandbyAt + "; " + c.outcome,
			})
		})
	}
}

// A standby that is in the status when the node workload starts is what
// the node workload finds, not a press it sees arrive, so it sends
// nothing and says so once. A restart of either workload must not turn
// the TV off.
func TestAStandbyFoundAtStartSendsNothing(t *testing.T) {
	fastWake(t)
	off := pressedOff(wokeAt(time.Now().Add(-time.Minute)))

	api, wire, log := awakeAtStart(t, televisionTV(cec.PowerOn), off)
	quiet(api)

	mustMatch(t, sentOf(wire, cec.OpStandby), 0)
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.StandbyAt, "")
	mustDeepEqual(t, linesWith(log, "turned Player"), []string{
		"Television lounge: the remote's power button turned Player media/den's room off at " + off.StandbyAt +
			"; the standby was in the status when the node workload started, so the adapter on node-1 sends nothing for it",
	})
}

// A standby runs once. A new node workload after the adapter comes back
// finds the started mark and sends nothing more for it.
func TestAStandbyRunsOnce(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	woke := wokeAt(time.Now().Add(-time.Minute))
	api := startCECAPI(t)
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(waking(woke))
	done := startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	off := pressedOff(woke)
	api.putTelevision(waking(off))
	stoodBy(t, api, off)
	wire.Add(televisionTV(cec.PowerOn))

	adapter.Unplug()
	<-done
	_, again := usbAdapter(wire)
	startNode(t, api, "node-1", again)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	quiet(api)

	mustMatch(t, sentOf(wire, cec.OpStandby), 1)
	peer, _ := wire.Peer(0)
	mustMatch(t, peer.Power, cec.PowerOn)
}

// Only a power press turns the TV off. A session that goes to sleep,
// such as at an idle timeout or when the panel goes dark, and a session
// that is lifted send the TV nothing, because a TV in a living room
// shows other inputs while the room's player is idle.
func TestASleepOrALiftSendsTheTVNothing(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, api *cecAPI, woke *TelevisionSession)
	}{
		{"a session that sleeps", func(t *testing.T, api *cecAPI, woke *TelevisionSession) {
			asleep := *woke
			asleep.Awake = false
			api.putTelevision(waking(&asleep))
		}},
		{"a session that is lifted", func(t *testing.T, api *cecAPI, _ *TelevisionSession) {
			mustSucceed(t, ApplyTelevisionSession(api.client, "lounge", nil))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			woke := wokeAt(time.Now().Add(-time.Minute))
			api, wire, _ := awakeAtStart(t, televisionTV(cec.PowerOn), woke)

			c.change(t, api, woke)
			quiet(api)

			mustMatch(t, sentOf(wire, cec.OpStandby), 0)
			peer, _ := wire.Peer(0)
			mustMatch(t, peer.Power, cec.PowerOn)
			television, _ := api.television("lounge")
			mustMatch(t, television.Status.StandbyAt, "")
		})
	}
}

// A press that turns the room on while the TV goes to standby is newer,
// so the standby stops and records it, and the wake runs.
func TestAWakeStopsAStandby(t *testing.T) {
	fastWake(t)
	stubborn := stubbornTV()
	stubborn.Power = cec.PowerOn
	woke := wokeAt(time.Now().Add(-time.Minute))
	api, wire, _ := awakeAtStart(t, stubborn, woke)
	off := pressedOff(woke)
	api.putTelevision(waking(off))
	api.waitUntil(t, "the first Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })

	again := wokeNow()
	api.putTelevision(waking(again))

	television := stoodBy(t, api, off)
	applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
	mustMatch(t, applied.Reason, reasonStopped)
	mustMatch(t, timeless([]string{applied.Message})[0],
		"the adapter on node-1 sent Standby to the TV once, and the standby stopped after <time>, before the TV reported Standby")
	api.waitForTelevision(t, "lounge", func(television Television) bool { return television.Status.WokeAt == again.WokeAt })
}

// spec.power goes first. A standby waits for a generation that is not
// applied yet, and a new generation during a standby stops it for good.
func TestAPendingPowerComesBeforeAStandby(t *testing.T) {
	fastWake(t)
	stubborn := stubbornTV()
	stubborn.Power = cec.PowerOn
	woke := wokeAt(time.Now().Add(-time.Minute))
	api, wire, log := awakeAtStart(t, stubborn, woke)
	off := pressedOff(woke)
	api.putTelevision(waking(off))
	api.waitUntil(t, "the first Standby", func() bool { return sentOf(wire, cec.OpStandby) == 1 })

	api.putTelevision(lounge(TelevisionOn))

	television := stoodBy(t, api, off)
	applied := conditionOf(television.Status.Conditions, conditionStandbyApplied)
	mustMatch(t, applied.Reason, reasonSuperseded)
	mustMatch(t, applied.Message, "generation 2 of spec.power asks On, so the standby stopped, and the adapter on node-1 sends nothing more for it")
	mustDeepEqual(t, linesWith(log, "turned Player"), []string{
		"Television lounge: the remote's power button turned Player media/den's room off at " + off.StandbyAt + "; " + applied.Message,
	})
}

// A standby that waits longer than the bound, here behind a spec.power
// that does not settle, sends nothing and is recorded.
func TestAStandbyThatWaitsTooLongSendsNothing(t *testing.T) {
	fastWake(t)
	fresh := cecWakeFresh
	cecWakeFresh = 100 * time.Millisecond
	t.Cleanup(func() { cecWakeFresh = fresh })
	woke := wokeAt(time.Now().Add(-time.Minute))
	api, wire, log := awakeAtStart(t, stubbornTV(), woke)
	off := pressedOff(woke)
	television := waking(off)
	television.Spec.Power = TelevisionOn

	api.putTelevision(television)

	stood := stoodBy(t, api, off)
	applied := conditionOf(stood.Status.Conditions, conditionStandbyApplied)
	mustMatch(t, applied.Reason, reasonTooLate)
	mustMatch(t, sentOf(wire, cec.OpStandby), 0)
	mustMatch(t, len(linesWith(log, "so the standby waits for it")), 1)
}
