package main

// The node workload's wake of a Television's session: the TV wakes,
// the session's Display becomes the active source, a source that takes
// the input during the guard gets it taken back a bounded number of
// times, and each wake runs at most once. The node workload acts only
// on a wake it sees arrive, so each test starts the node workload
// first and then wakes the room. cecnode_television_test.go holds the
// TV fixtures.

import (
	"strings"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// fastWake shortens the power timers and the guard, so a test that
// waits for a whole wake finishes in well under a second.
func fastWake(t *testing.T) {
	t.Helper()
	fastPower(t)
	guard, settle := cecWakeGuard, cecWakeSettle
	cecWakeGuard, cecWakeSettle = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { cecWakeGuard, cecWakeSettle = guard, settle })
}

// wokeNow is the session a Receiver's session writes when it wakes the
// room on node-1's Display.
func wokeNow() *TelevisionSession {
	return wokeAt(time.Now())
}

func wokeAt(when time.Time) *TelevisionSession {
	return &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: when.UTC().Format(wakeTimeLayout)}
}

// waking is the den bus's Television with the session the Deployment
// writes in its status.
func waking(session *TelevisionSession) Television {
	television := lounge("")
	television.Status.Session = session
	return television
}

// wakeLive writes a session's wake after the node workload has read
// the Televisions once, which is a wake the node workload sees arrive.
func wakeLive(t *testing.T, api *cecAPI, session *TelevisionSession) {
	t.Helper()
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	api.putTelevision(waking(session))
}

// awakeRoom runs the node workload for node-1 on a bus in Control and
// then wakes the room with a session.
func awakeRoom(t *testing.T, wire *cectest.Bus, session *TelevisionSession) *cecAPI {
	t.Helper()
	api := controlling(t, wire, lounge(""))
	wakeLive(t, api, session)
	return api
}

// startedAt answers the status a wake's started mark leaves: the wake
// is named, and the condition says it began.
func startedAt(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason == reasonWaking
	})
}

// streamer is a streaming player on the receiver's input 5 that claims
// Active Source after another device's claim, as many times as grabs.
func streamer(grabs int, after time.Duration) cectest.Peer {
	return cectest.Peer{Logical: 8, Physical: 0x1500, PrimaryType: 4, OSDName: "Player", Power: cec.PowerOn, Grabs: grabs, GrabAfter: after}
}

// wokeWith waits until the node workload recorded the result of a
// session's wake.
func wokeWith(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason != reasonWaking
	})
}

// claimsOf answers the Active Source messages the adapters sent, in
// order, as the log writes them.
func claimsOf(wire *cectest.Bus) []string {
	var claims []string
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpActiveSource && !message.IsPoll() {
			claims = append(claims, message.String())
		}
	}
	return claims
}

func TestTheWakeWakesTheTVAndShowsTheDisplay(t *testing.T) {
	cases := []struct {
		name    string
		tv      cectest.Peer
		wakes   int
		message string
	}{
		{"a TV in standby", televisionTV(cec.PowerStandby), 1,
			"the TV reported On after the adapter on node-1 sent Image View On once; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 200 ms after; the last Active Source on the bus is 1.3.0.0"},
		{"a TV that is on", televisionTV(cec.PowerOn), 0,
			"the TV already reported On, so the adapter on node-1 sent no command; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 200 ms after; the last Active Source on the bus is 1.3.0.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(c.tv)
			session := wokeNow()

			api := awakeRoom(t, wire, session)

			television := wokeWith(t, api, session)
			applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
			mustMatch(t, applied.Status, ConditionTrue)
			mustMatch(t, applied.Reason, reasonConfirmed)
			mustMatch(t, applied.Message, c.message)
			mustMatch(t, sentOf(wire, cec.OpImageViewOn), c.wakes)
			mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
			peer, _ := wire.Peer(0)
			mustMatch(t, peer.Power, cec.PowerOn)
			mustCommandTheTVAlone(t, wire)
		})
	}
}

// A streaming player that wakes with the room claims Active Source for
// itself. The adapter takes the input back during the guard, at most
// twice, and the status states what the bus reports at the end.
func TestTheWakeTakesTheInputBackFromAPlayer(t *testing.T) {
	cases := []struct {
		name    string
		grabs   int
		claims  []string
		status  ConditionStatus
		reason  string
		message string
	}{
		{"a player that claims once", 1, []string{"4->f 82 13 00", "4->f 82 13 00"}, ConditionTrue, reasonConfirmed,
			"the TV already reported On, so the adapter on node-1 sent no command; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, 2 times, because the source at 1.5.0.0 claimed the input once, first after <time>; the last Active Source on the bus is 1.3.0.0"},
		{"a player that always claims", 10, []string{"4->f 82 13 00", "4->f 82 13 00", "4->f 82 13 00"}, ConditionFalse, reasonTaken,
			"the TV already reported On, so the adapter on node-1 sent no command; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, 3 times, because the source at 1.5.0.0 claimed the input 3 times, first after <time>; the source at 1.5.0.0 holds the input, and the adapter sends Active Source at most 3 times for one wake"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(televisionTV(cec.PowerOn))
			wire.Add(streamer(c.grabs, 5*time.Millisecond))
			session := wokeNow()

			api := awakeRoom(t, wire, session)

			television := wokeWith(t, api, session)
			applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
			mustMatch(t, applied.Status, c.status)
			mustMatch(t, applied.Reason, c.reason)
			mustMatch(t, timeless([]string{applied.Message})[0], c.message)
			mustDeepEqual(t, claimsOf(wire), c.claims)
		})
	}
}

// A claim after the guard is a person's, or a source the person chose,
// and the adapter leaves it alone.
func TestAClaimAfterTheGuardStands(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	wire.Add(streamer(1, 2*cecWakeGuard))
	session := wokeNow()

	api := awakeRoom(t, wire, session)
	wokeWith(t, api, session)
	time.Sleep(3 * cecWakeGuard)
	api.nudge()
	time.Sleep(cecWakeGuard)

	mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	entry, _ := api.entry("den", "node-1")
	mustMatch(t, entry.ActiveSource, "1.5.0.0")
}

// A session that goes to sleep during the guard ends it: the room is
// off, and the adapter claims nothing in it.
func TestASessionThatSleepsEndsTheGuard(t *testing.T) {
	fastWake(t)
	cecWakeGuard = time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	wire.Add(streamer(1, 300*time.Millisecond))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	asleep := *session
	asleep.Awake = false
	api.putTelevision(waking(&asleep))
	time.Sleep(2 * cecWakeGuard)

	mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	television := wokeWith(t, api, session)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonStopped)
}

// Each wake runs once. A pass, and a new node workload after the
// adapter comes back, send nothing for a wake already run.
func TestAWakeRunsOnce(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	session := wokeNow()
	api := startCECAPI(t)
	adapter, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	done := startNode(t, api, "node-1", device)
	wakeLive(t, api, session)
	wokeWith(t, api, session)

	adapter.Unplug()
	<-done
	_, again := usbAdapter(wire)
	startNode(t, api, "node-1", again)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	api.nudge()
	time.Sleep(2 * cecWakeGuard)

	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	mustMatch(t, len(claimsOf(wire)), 1)
}

// A session that wakes the room again while it is awake, such as with
// the remote's power button, is a new wokeAt, and the TV wakes again.
func TestANewWakeWakesAgain(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	first := wokeNow()
	api := awakeRoom(t, wire, first)
	wokeWith(t, api, first)
	wire.Add(televisionTV(cec.PowerStandby))

	second := wokeAt(time.Now().Add(time.Second))
	api.putTelevision(waking(second))

	wokeWith(t, api, second)
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
	mustMatch(t, len(claimsOf(wire)), 2)
}

// What a session asks that sends nothing: a session that is asleep,
// and one that never woke.
func TestAWakeThatSendsNothing(t *testing.T) {
	asleep := wokeNow()
	asleep.Awake = false
	never := wokeNow()
	never.WokeAt = ""
	cases := []struct {
		name    string
		session *TelevisionSession
	}{
		{"a session that is asleep", asleep},
		{"a session that never woke", never},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fastWake(t)
			wire := roomWithTV(televisionTV(cec.PowerStandby))

			api := awakeRoom(t, wire, c.session)
			api.nudge()
			time.Sleep(2 * cecWakeGuard)

			mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
			mustMatch(t, len(claimsOf(wire)), 0)
			television, _ := api.television("lounge")
			mustMatch(t, television.Status.WokeAt, "")
		})
	}
}

// A wake that is in the status when the node workload starts is what
// the node workload finds, not a change it sees arrive, so it sends
// nothing, records nothing, and says so once. An operator restart must
// not wake the room.
func TestAWakeFoundAtStartSendsNothing(t *testing.T) {
	fastWake(t)
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	_, device := usbAdapter(wire)
	session := wokeNow()
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(waking(session))

	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	api.nudge()
	time.Sleep(2 * cecWakeGuard)

	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
	mustMatch(t, len(claimsOf(wire)), 0)
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.WokeAt, "")
	mustDeepEqual(t, linesWith(log, "Television lounge"), []string{
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt +
			"; the wake was in the status when the node workload started, so the adapter on node-1 sends nothing for it",
	})
}

// The node workload measures a wake's age on its own clock, from when it
// first saw the wake, and never from the Deployment's time in wokeAt,
// because the two machines' clocks can differ.
func TestAWakeIsTimedOnTheNodesClock(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	session := wokeAt(time.Now().Add(-time.Hour))

	api := awakeRoom(t, wire, session)

	television := wokeWith(t, api, session)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Status, ConditionTrue)
}

// A wake that waits longer than the bound, here behind a spec.power
// that does not settle, sends nothing and is recorded, so no later pass
// runs it.
func TestAWakeThatWaitsTooLongSendsNothing(t *testing.T) {
	fastWake(t)
	fresh := cecWakeFresh
	cecWakeFresh = 100 * time.Millisecond
	t.Cleanup(func() { cecWakeFresh = fresh })
	stubborn := stubbornTV()
	stubborn.Power = cec.PowerOn
	wire := roomWithTV(stubborn)
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	session := wokeNow()
	television := waking(session)
	television.Spec.Power = TelevisionStandby

	api.putTelevision(television)

	woken := wokeWith(t, api, session)
	applied := conditionOf(woken.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonTooLate)
	mustMatch(t, timeless([]string{applied.Message})[0][:40], "the adapter on node-1 first saw the wake")
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
	mustMatch(t, len(claimsOf(wire)), 0)
}

// A session that goes to sleep while the TV wakes stops the wake. Its
// line says the wake stopped, and the status records it as Stopped.
func TestTheNodeLogsAWakeThatStops(t *testing.T) {
	fastWake(t)
	api := startCECAPI(t)
	wire := roomWithTV(stubbornTV())
	_, device := usbAdapter(wire)
	session := wokeNow()
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	log := loggedNode(t, api, device)
	wakeLive(t, api, session)
	api.waitUntil(t, "the first Image View On", func() bool { return sentOf(wire, cec.OpImageViewOn) == 1 })

	asleep := *session
	asleep.Awake = false
	api.putTelevision(waking(&asleep))

	stopped := "the adapter on node-1 sent Image View On to the TV, and the wake stopped after <time>"
	mustDeepEqual(t, waitForLines(t, log, "Television lounge", 2), []string{
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; the adapter on node-1 starts the wake",
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; " + stopped,
	})
	television := wokeWith(t, api, session)
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonStopped)
	mustMatch(t, timeless([]string{applied.Message})[0], stopped)
	mustMatch(t, len(claimsOf(wire)), 0)
}

// The adapter that speaks for the session's Display runs the wake, and
// the other adapter of the bus sends nothing for it, so the Active
// Source states that Display's address.
func TestTheDisplaysAdapterRunsTheWake(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	api := startCECAPI(t)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putDisplay("bnq-0002-monitor", "node-2", "1.4.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}))
	api.putTelevision(lounge(""))
	for _, machine := range []string{"node-1", "node-2"} {
		_, device := usbAdapter(wire)
		startNode(t, api, machine, device)
		api.waitForEntry(t, "den", machine, func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	}
	second, _ := api.entry("den", "node-2")
	session := wokeNow()
	session.Display = "bnq-0002-monitor"

	api.putTelevision(waking(session))

	wokeWith(t, api, session)
	var senders []cec.LogicalAddress
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); (opcode == cec.OpImageViewOn || opcode == cec.OpActiveSource) && !message.IsPoll() {
			senders = append(senders, message.From)
		}
	}
	own := cec.LogicalAddress(*second.LogicalAddress)
	mustDeepEqual(t, senders, []cec.LogicalAddress{own, own})
	mustDeepEqual(t, claimsOf(wire), []string{cec.ActiveSource(own, 0x1400).String()})
}

// The last Active Source on the bus reaches the adapter's entry and the
// Television's status, whether the adapter sent it or heard it, and in
// Listen too. The Television also names the Display at that address,
// and names none for a source that is no Display.
func TestTheActiveSourceReachesTheTelevision(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	wokeWith(t, api, session)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.3.0.0" })
	passes(t, api, 1)
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.ActiveSource, "1.3.0.0")
	mustMatch(t, television.Status.ActiveDisplay, "acm-0001-receiver")

	wire.Send(cec.ActiveSource(8, 0x1500))

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })
	passes(t, api, 1)
	television, _ = api.television("lounge")
	mustMatch(t, television.Status.ActiveSource, "1.5.0.0")
	mustMatch(t, television.Status.ActiveDisplay, "")
}

func TestAListeningAdapterReportsTheActiveSource(t *testing.T) {
	api := startCECAPI(t)
	wire := cecRoom()
	_, device := usbAdapter(wire)
	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	startNode(t, api, "node-1", device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

	wire.Send(cec.ActiveSource(8, 0x1500))

	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })
}

// Each wake is two lines, one when it starts and one when it ends, and
// the passes and reads after it are quiet.
func TestTheNodeLogsEachWakeOnce(t *testing.T) {
	fastWake(t)
	api := startCECAPI(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	wire.Add(streamer(1, 5*time.Millisecond))
	_, device := usbAdapter(wire)
	session := wokeNow()
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	log := loggedNode(t, api, device)
	wakeLive(t, api, session)

	wokeWith(t, api, session)
	api.nudge()
	time.Sleep(2 * cecWakeGuard)

	mustDeepEqual(t, timeless(linesWith(log, "Television lounge")), []string{
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; the adapter on node-1 starts the wake",
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt +
			"; the adapter on node-1 sent Image View On to the TV once; the TV reported On after <time>" +
			"; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, 2 times, because the source at 1.5.0.0 claimed the input once, first after <time>; the last Active Source on the bus is 1.3.0.0" +
			"; the wake ended after <time>",
	})
}

// A session that goes to sleep while the adapter waits to take the
// input back ends the wake before the second Active Source.
func TestASleepDuringTheSettleClaimsNothingMore(t *testing.T) {
	fastWake(t)
	cecWakeGuard, cecWakeSettle = 2*time.Second, time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	wire.Add(streamer(1, 5*time.Millisecond))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })

	asleep := *session
	asleep.Awake = false
	api.putTelevision(waking(&asleep))
	time.Sleep(2 * cecWakeSettle)

	mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
}

// spec.power goes first. A new generation of spec.power during a wake
// stops the wake for good, with one line, and the power is applied.
func TestANewPowerGenerationSupersedesAWake(t *testing.T) {
	fastWake(t)
	cecWakeGuard = 2 * time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	wire.Add(streamer(1, time.Second))
	session := wokeNow()
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	log := loggedNode(t, api, device)
	wakeLive(t, api, session)
	api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	api.putTelevision(lounge(TelevisionStandby))

	television := wokeWith(t, api, session)
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonSuperseded)
	mustMatch(t, applied.Message, "generation 2 of spec.power asks Standby, so the wake stopped, and the adapter on node-1 sends nothing more for it")
	appliedAt(t, api, "lounge", 2)
	time.Sleep(cecWakeGuard)
	mustMatch(t, len(claimsOf(wire)), 1)
	peer, _ := wire.Peer(0)
	mustMatch(t, peer.Power, cec.PowerStandby)
	mustDeepEqual(t, linesWith(log, "woke the room"), []string{
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; the adapter on node-1 starts the wake",
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; " + applied.Message,
	})
	// No Active Source follows the first Standby.
	standby := false
	for _, message := range wire.Sent() {
		opcode, _ := message.Opcode()
		standby = standby || opcode == cec.OpStandby
		if standby && opcode == cec.OpActiveSource {
			t.Errorf("Active Source %v went out after the Standby", message)
		}
	}
}

// A wake that arrives while spec.power is not applied yet waits for
// it, and says so once: every power command goes out before the
// wake's first command.
func TestAWakeWaitsForAPendingPower(t *testing.T) {
	fastWake(t)
	slow := televisionTV(cec.PowerOn)
	slow.Transition = 20
	wire := roomWithTV(slow)
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.putTelevision(lounge(""))
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	session := wokeNow()
	television := waking(session)
	television.Spec.Power = TelevisionStandby

	api.putTelevision(television)

	woken := wokeWith(t, api, session)
	mustMatch(t, woken.Status.PowerGeneration, int64(2))
	mustMatch(t, conditionOf(woken.Status.Conditions, conditionWakeApplied).Status, ConditionTrue)
	var order []cec.Opcode
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); !message.IsPoll() && (opcode == cec.OpStandby || opcode == cec.OpImageViewOn) {
			order = append(order, opcode)
		}
	}
	mustDeepEqual(t, order, []cec.Opcode{cec.OpStandby, cec.OpImageViewOn})
	peer, _ := wire.Peer(0)
	mustMatch(t, peer.Power, cec.PowerOn)
	mustDeepEqual(t, linesWith(log, "waits for it"), []string{
		"Television lounge: Player media/den's session woke the room at " + session.WokeAt +
			"; generation 2 of spec.power asks Standby and is not applied yet, so the wake waits for it",
	})
}

// A wake stopped by a change of mode records that it stopped, and the
// bus's return to Control does not run it again: its started mark names
// the wake, so its Image View On and Active Source go out once.
func TestAStoppedWakeDoesNotRunAgain(t *testing.T) {
	fastWake(t)
	cecWakeGuard = time.Second
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })
	api.nudge()
	time.Sleep(2 * cecWakeGuard)

	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	mustMatch(t, len(claimsOf(wire)), 1)
	television, _ := api.television("lounge")
	mustMatch(t, television.Status.WokeAt, session.WokeAt)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonStopped)
}

// A started mark the API server refuses sends nothing, and the wake
// runs once the server takes the mark.
func TestAWakeWaitsForItsStartedMark(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerStandby))
	api := controlling(t, wire, lounge(""))
	api.refuseWakeWrites(true)
	session := wokeNow()
	wakeLive(t, api, session)
	api.nudge()
	time.Sleep(4 * cecPowerWindow)
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)

	api.refuseWakeWrites(false)
	api.nudge()

	startedAt(t, api, session)
	wokeWith(t, api, session)
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
}

// A Standby another device sent the TV is a recent command. The TV can
// still answer On for a while after it, so the wake sends Image View On
// without trusting that read.
func TestAWakeAfterAHeardStandbySendsImageViewOn(t *testing.T) {
	fastWake(t)
	lagging := televisionTV(cec.PowerOn)
	lagging.Lag = 3
	wire := roomWithTV(lagging)
	api := controlling(t, wire, lounge(""))
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterScanned })

	wire.Send(cec.Standby(5, cec.AddressBroadcast))
	time.Sleep(20 * time.Millisecond)
	session := wokeNow()
	api.putTelevision(waking(session))

	television := wokeWith(t, api, session)
	mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Status, ConditionTrue)
	mustMatch(t, sentOf(wire, cec.OpImageViewOn), 1)
	peer, _ := wire.Peer(0)
	mustMatch(t, peer.Power, cec.PowerOn)
}

// The wake reads the TV's power again at the end of the guard. A TV
// that went to standby during the guard is not a confirmed wake.
func TestTheWakeConfirmsThePowerAtTheEndOfTheGuard(t *testing.T) {
	fastWake(t)
	wire := roomWithTV(televisionTV(cec.PowerOn))
	session := wokeNow()
	api := awakeRoom(t, wire, session)
	api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	wire.Send(cec.Standby(5, cec.AddressTV))

	television := wokeWith(t, api, session)
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonUnconfirmed)
	if !strings.HasSuffix(applied.Message, "; at the end of the guard the TV reported Standby") {
		t.Errorf("the message does not end with the last power read: %s", applied.Message)
	}
}

// An Active Source from another adapter of the bus is a later wake of
// the bus, so the guard ends at once and claims nothing more.
func TestAnotherAdaptersClaimEndsTheGuard(t *testing.T) {
	fastWake(t)
	cecWakeGuard = 2 * time.Second
	wire := roomWithTV(televisionTV(cec.PowerOn))
	api := startCECAPI(t)
	_, device := usbAdapter(wire)
	api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
	api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}))
	other := scannedEntry("node-2", 8)
	other.ReportedAt = timestamp(time.Now())
	mustSucceed(t, ApplyCECAdapterStatus(api.client, "den", "node-2", &other))
	api.putTelevision(lounge(""))
	startNode(t, api, "node-1", device)
	session := wokeNow()
	wakeLive(t, api, session)
	api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

	wire.Send(cec.ActiveSource(8, 0x1400))

	television := wokeWith(t, api, session)
	applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
	mustMatch(t, applied.Reason, reasonSuperseded)
	mustMatch(t, timeless([]string{applied.Message})[0],
		"the TV already reported On, so the adapter on node-1 sent no command; the adapter on node-1 sent Active Source for 1.3.0.0, Display acm-0001-receiver, once, and the adapter on node-2 sent Active Source for 1.4.0.0 after <time>, so the guard ended")
	mustMatch(t, len(claimsOf(wire)), 1)
}
