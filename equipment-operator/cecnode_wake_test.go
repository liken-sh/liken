package main

// The node workload acts only on a wake that arrives while it runs, so
// each test starts the node workload first and then wakes the room.
// cecnode_television_test.go has the TV fixtures.

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// wokeNow returns the session status that the Deployment writes when a
// Receiver's session wakes the room on node-1's Display.
func wokeNow() *TelevisionSession {
	return wokeAt(time.Now())
}

func wokeAt(when time.Time) *TelevisionSession {
	return &TelevisionSession{Player: "media/den", Display: "acm-0001-receiver", Awake: true, WokeAt: when.UTC().Format(wakeTimeLayout)}
}

// waking returns the Television lounge with session in its status.
func waking(session *TelevisionSession) Television {
	television := lounge("")
	television.Status.Session = session
	return television
}

// wakeLive writes a session's wake after the node workload has listed
// the Televisions once, so the wake arrives while the node workload
// runs.
func wakeLive(t *testing.T, api *cecAPI, session *TelevisionSession) {
	t.Helper()
	api.scanned(t, "node-1")
	api.putTelevision(waking(session))
}

// awakeRoom starts the node workload for node-1 on a bus in Control,
// then wakes the room with session.
func awakeRoom(t *testing.T, wire *cectest.Bus, session *TelevisionSession) *cecAPI {
	t.Helper()
	api := controlling(t, wire, lounge(""))
	wakeLive(t, api, session)
	return api
}

// startedAt waits until the Television has the started mark of a wake:
// status.wokeAt is the session's wokeAt, and WakeApplied has the reason
// Waking.
func startedAt(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevision(t, "lounge", func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason == reasonWaking
	})
}

// streamer returns a streaming player at 1.5.0.0. Each time another
// device sends Active Source, the player sends its own after the delay
// after, up to grabs times.
func streamer(grabs int, after time.Duration) cectest.Peer {
	return cectest.Peer{Logical: 8, Physical: 0x1500, PrimaryType: 4, OSDName: "Player", Power: cec.PowerOn, Grabs: grabs, GrabAfter: after}
}

// wokeWith waits until the node workload has written the result of a
// session's wake, and returns the Television. The wait covers the
// longest wake: cecWakeFresh before it starts, the guard, and the
// System Audio Mode Request after the guard.
func wokeWith(t *testing.T, api *cecAPI, session *TelevisionSession) Television {
	t.Helper()
	return api.waitForTelevisionWithin(t, "lounge", cecWakeFresh+cecWakeGuard+cecSystemAudioWait+testTimeout, func(television Television) bool {
		return television.Status.WokeAt == session.WokeAt && conditionOf(television.Status.Conditions, conditionWakeApplied).Reason != reasonWaking
	})
}

// claimsOf returns the Active Source messages that the adapters sent,
// in order, in the form the log uses.
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
		{"a TV in standby", televisionTV(cec.PowerStandby), 2,
			"The TV reported On after the adapter on node-1 sent Image View On once. The adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 30 s after. The last Active Source on the bus is 1.3.0.0"},
		{"a TV that is on", televisionTV(cec.PowerOn), 1,
			"The TV already reported On. The adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, once, and no other source claimed the input in the 30 s after. The last Active Source on the bus is 1.3.0.0"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(c.tv)
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
		})
	}
}

// A streaming player that wakes with the room claims Active Source for
// itself. The adapter takes the input back during the guard, at most
// cecWakeReclaims times, and the status reports which source holds the
// input at the end.
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
			"The TV already reported On. The adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, 2 times, because the source at 1.5.0.0 claimed the input once, first after <time>. The last Active Source on the bus is 1.3.0.0"},
		{"a player that always claims", 10, []string{"4->f 82 13 00", "4->f 82 13 00", "4->f 82 13 00"}, ConditionFalse, reasonTaken,
			"The TV already reported On. The adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, 3 times, because the source at 1.5.0.0 claimed the input 3 times, first after <time>. The source at 1.5.0.0 holds the input, and the adapter sends the pair at most 3 times for one wake"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerOn))
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
		})
	}
}

// A claim after the guard comes from a person or from a source the
// person chose, so the adapter leaves it alone.
func TestAClaimAfterTheGuardIsLeftAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
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
	})
}

// A session that goes to sleep during the guard ends the guard,
// because the room is off and the adapter claims nothing in it.
func TestASessionThatSleepsEndsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
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
	})
}

// Each wake runs once. Neither a later pass nor a new node workload,
// started after the adapter comes back, sends anything for a wake that
// already ran.
func TestAWakeRunsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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
		api.scanned(t, "node-1")
		api.nudge()
		time.Sleep(2 * cecWakeGuard)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
		mustMatch(t, len(claimsOf(wire)), 1)
	})
}

// A session that wakes the room again while it is awake, for example
// from the remote's power button, has a new wokeAt, so the TV wakes
// again.
func TestANewWakeWakesAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		first := wokeNow()
		api := awakeRoom(t, wire, first)
		wokeWith(t, api, first)
		wire.Add(televisionTV(cec.PowerStandby))

		second := wokeAt(time.Now().Add(time.Second))
		api.putTelevision(waking(second))

		wokeWith(t, api, second)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 4)
		mustMatch(t, len(claimsOf(wire)), 2)
	})
}

// A session that is asleep, and a session with no wokeAt, send
// nothing.
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
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithTV(televisionTV(cec.PowerStandby))

				api := awakeRoom(t, wire, c.session)
				api.nudge()
				time.Sleep(2 * cecWakeGuard)

				mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
				mustMatch(t, len(claimsOf(wire)), 0)
				television, _ := api.television("lounge")
				mustMatch(t, television.Status.WokeAt, "")
			})
		})
	}
}

// A wake that is already in the status when the node workload starts
// did not arrive while the node workload ran. The node workload sends
// nothing for it, records nothing, and logs that once, so a restart
// never wakes the room.
func TestAWakeFoundAtStartSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
		_, device := usbAdapter(wire)
		session := wokeNow()
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(waking(session))

		log := loggedNode(t, api, device)
		api.scanned(t, "node-1")
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
	})
}

// The node workload measures a wake's age on its own clock, from when
// it first saw the wake, and never from the time that the Deployment
// wrote in wokeAt, because the two machines' clocks can differ.
func TestAWakeIsTimedOnTheNodesClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		session := wokeAt(time.Now().Add(-time.Hour))

		api := awakeRoom(t, wire, session)

		television := wokeWith(t, api, session)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Status, ConditionTrue)
	})
}

// A wake that waits longer than cecWakeFresh sends nothing and is
// recorded, so no later pass runs it. Here it waits because the API
// server refuses the status write that applies spec.power.
func TestAWakeThatWaitsTooLongSendsNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		api.refusePowerWrites(true)
		session := wokeNow()
		television := waking(session)
		television.Spec.Power = TelevisionStandby

		api.putTelevision(television)
		time.Sleep(cecWakeFresh + time.Second)
		api.nudge()

		woken := wokeWith(t, api, session)
		applied := conditionOf(woken.Status.Conditions, conditionWakeApplied)
		mustMatch(t, applied.Reason, reasonTooLate)
		mustMatch(t, applied.Message, "The adapter on node-1 first saw the wake 121 s ago, which is more than 120 s, so it sent nothing")
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 0)
		mustMatch(t, len(claimsOf(wire)), 0)
	})
}

// A session that goes to sleep while the TV wakes stops the wake. The
// log line says the wake stopped, and the status records the reason
// Stopped.
func TestTheNodeLogsAWakeThatStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(stubbornTV())
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
		mustMatch(t, timeless([]string{applied.Message})[0], "The adapter on node-1 sent Image View On to the TV, and the wake stopped after <time>")
		mustMatch(t, len(claimsOf(wire)), 0)
	})
}

// The adapter that speaks for the session's Display runs the wake, and
// the other adapter on the bus sends nothing for it, so the Active
// Source carries that Display's address.
func TestTheDisplaysAdapterRunsTheWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		api := startCECAPI(t)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putDisplay("bnq-0002-monitor", "node-2", "1.4.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}, CECBusAdapter{Machine: "node-2", Display: "bnq-0002-monitor"}))
		api.putTelevision(lounge(""))
		for _, machine := range []string{"node-1", "node-2"} {
			_, device := usbAdapter(wire)
			startNode(t, api, machine, device)
			api.scanned(t, machine)
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
		mustDeepEqual(t, senders, []cec.LogicalAddress{own, own, own})
		mustDeepEqual(t, claimsOf(wire), []string{cec.ActiveSource(own, 0x1400).String()})
	})
}

// The last Active Source on the bus appears in the adapter's entry and
// in the Television's status, whether the adapter sent it or received
// it, and in Listen mode too. The Television also names the Display
// at that address, and names none for a source that is not a Display.
func TestTheActiveSourceReachesTheTelevision(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
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
	})
}

func TestAListeningAdapterReportsTheActiveSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := fakeCECBus()
		_, device := usbAdapter(wire)
		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		startNode(t, api, "node-1", device)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })

		wire.Send(cec.ActiveSource(8, 0x1500))

		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })
	})
}

// A wake logs two lines, one when it starts and one when it ends. Later
// passes and reads log nothing.
func TestTheNodeLogsEachWakeOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
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
				"; the adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, 2 times, because the source at 1.5.0.0 claimed the input once, first after <time>; the last Active Source on the bus is 1.3.0.0" +
				"; the wake ended after <time>",
		})
	})
}

// A session that goes to sleep while the adapter waits to take the
// input back ends the wake before the second Active Source.
func TestASleepDuringTheSettleClaimsNothingMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		wire.Add(streamer(1, 5*time.Millisecond))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.ActiveSource == "1.5.0.0" })

		asleep := *session
		asleep.Awake = false
		api.putTelevision(waking(&asleep))
		time.Sleep(2 * cecWakeSettle)

		mustDeepEqual(t, claimsOf(wire), []string{"4->f 82 13 00"})
	})
}

// spec.power takes priority. A new generation of spec.power during a
// wake stops the wake for good and logs one line, and the power is
// applied.
func TestANewPowerGenerationSupersedesAWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
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
		mustMatch(t, applied.Message, "spec.power at generation 2 is Standby, so the wake stopped, and the adapter on node-1 sends nothing more for it")
		appliedAt(t, api, "lounge", 2)
		time.Sleep(cecWakeGuard)
		mustMatch(t, len(claimsOf(wire)), 1)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerStandby)
		mustDeepEqual(t, linesWith(log, "woke the room"), []string{
			"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; the adapter on node-1 starts the wake",
			"Television lounge: Player media/den's session woke the room at " + session.WokeAt + "; " + applied.Message,
		})
		// The wake sends no Active Source after the Standby.
		standby := false
		for _, message := range wire.Sent() {
			opcode, _ := message.Opcode()
			standby = standby || opcode == cec.OpStandby
			if standby && opcode == cec.OpActiveSource {
				t.Errorf("the wire carries Active Source %v after Standby, want none", message)
			}
		}
	})
}

// A wake that arrives while spec.power is not applied yet waits for
// it and logs that once. Every power command goes out before the
// wake's first command.
func TestAWakeWaitsForAPendingPower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		slow := televisionTV(cec.PowerOn)
		slow.Transition = 20
		wire := busWithTV(slow)
		api := startCECAPI(t)
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(""))
		log := loggedNode(t, api, device)
		api.scanned(t, "node-1")
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
		mustDeepEqual(t, order, []cec.Opcode{cec.OpStandby, cec.OpImageViewOn, cec.OpImageViewOn})
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerOn)
		mustDeepEqual(t, linesWith(log, "waits for it"), []string{
			"Television lounge: Player media/den's session woke the room at " + session.WokeAt +
				"; spec.power at generation 2 is Standby and is not applied yet, so the wake waits for it",
		})
	})
}

// A wake stopped by a change of mode records that it stopped. When the
// bus returns to Control, the wake does not run again, because its
// started mark names it, so its Image View On and Active Source go out
// once.
func TestAStoppedWakeDoesNotRunAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		api.putBus(CECBus{Metadata: ObjectMeta{Name: "den"}, Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: "node-1"}}}})
		api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.scanned(t, "node-1")
		api.nudge()
		time.Sleep(2 * cecWakeGuard)

		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
		mustMatch(t, len(claimsOf(wire)), 1)
		television, _ := api.television("lounge")
		mustMatch(t, television.Status.WokeAt, session.WokeAt)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonStopped)
	})
}

// When the API server refuses the started mark, the wake sends
// nothing. It runs once the server accepts the mark.
func TestAWakeWaitsForItsStartedMark(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerStandby))
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
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
	})
}

// After another device broadcasts Standby, the TV can still report On
// for a while, so the wake sends Image View On without trusting that
// power read.
func TestAWakeAfterAStandbyFromAnotherDeviceSendsImageViewOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lagging := televisionTV(cec.PowerOn)
		lagging.Lag = 3
		wire := busWithTV(lagging)
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")

		wire.Send(cec.Standby(5, cec.AddressBroadcast))
		time.Sleep(20 * time.Millisecond)
		session := wokeNow()
		api.putTelevision(waking(session))

		television := wokeWith(t, api, session)
		applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
		mustMatch(t, applied.Status, ConditionTrue)
		mustMatch(t, strings.HasPrefix(applied.Message, "The TV reported On after the adapter on node-1 sent Image View On once. "), true)
		mustMatch(t, sentOf(wire, cec.OpImageViewOn), 2)
		peer, _ := wire.Peer(0)
		mustMatch(t, peer.Power, cec.PowerOn)
	})
}

// The wake reads the TV's power again at the end of the guard. A TV
// that went to standby during the guard is not a confirmed wake.
func TestTheWakeConfirmsThePowerAtTheEndOfTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
		session := wokeNow()
		api := awakeRoom(t, wire, session)
		api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.Standby(5, cec.AddressTV))

		television := wokeWith(t, api, session)
		applied := conditionOf(television.Status.Conditions, conditionWakeApplied)
		mustMatch(t, applied.Reason, reasonUnconfirmed)
		if !strings.HasSuffix(applied.Message, ". At the end of the guard the TV reported Standby") {
			t.Errorf("the message is %q, want it to end with \". At the end of the guard the TV reported Standby\"", applied.Message)
		}
	})
}

// An Active Source from another adapter on the bus is a later wake on
// that bus, so the guard ends at once and the adapter claims nothing
// more.
func TestAnotherAdaptersClaimEndsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithTV(televisionTV(cec.PowerOn))
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
			"The TV already reported On. The adapter on node-1 sent Image View On and Active Source for 1.3.0.0, Display acm-0001-receiver, once, and the adapter on node-2 sent Active Source for 1.4.0.0 after <time>, so the guard ended")
		mustMatch(t, len(claimsOf(wire)), 1)
	})
}
