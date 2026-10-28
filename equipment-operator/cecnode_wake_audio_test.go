package main

// The System Audio Mode Request in the wake. When the session's Display
// passes through a receiver, the wake asks the receiver for System
// Audio Mode with the Display's physical address, once, after its
// guard, and SystemAudioApplied states what the receiver answered. A
// wake whose Display passes through no receiver sends no request.
// cecnode_wake_test.go holds the wake fixtures, and
// cecnode_systemaudio_test.go holds the receiver.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
	"github.com/liken-sh/liken/kubernetes/events"
)

// throughReceiver is the session's Display as the Deployment lists it,
// passing through the den Receiver.
var throughReceiver = []TelevisionDisplay{
	{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}},
}

// straightOn is the session's Display straight on the TV.
var straightOn = []TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}

// wakingThrough is the waking Television with the Displays the
// Deployment lists.
func wakingThrough(session *TelevisionSession, displays []TelevisionDisplay) Television {
	television := waking(session)
	television.Status.Displays = displays
	return television
}

// wokeThrough runs the node workload on a wire, wakes the room with the
// Displays the Deployment lists, and waits for the wake's result.
func wokeThrough(t *testing.T, wire *cectest.Bus, displays []TelevisionDisplay) (*cecAPI, Television) {
	t.Helper()
	session := wokeNow()
	api := controlling(t, wire, lounge(""))
	api.scanned(t, "node-1")
	api.putTelevision(wakingThrough(session, displays))
	return api, wokeWith(t, api, session)
}

// framesOf answers the messages the adapters sent with one opcode, as
// the log writes them.
func framesOf(wire *cectest.Bus, opcode cec.Opcode) []string {
	var frames []string
	for _, message := range wire.Sent() {
		if sent, _ := message.Opcode(); sent == opcode && !message.IsPoll() {
			frames = append(frames, message.String())
		}
	}
	return frames
}

// The request carries the Display's physical address, so the receiver
// switches to the Display's input. The receiver's answer decides the
// condition: a broadcast [On] after the request, or, with none in 5
// seconds, one Give System Audio Mode Status after the scan's own.
func TestTheWakeAsksTheReceiverForSystemAudioMode(t *testing.T) {
	cases := []struct {
		name    string
		reply   cectest.AudioReply
		status  ConditionStatus
		reason  string
		reads   int
		message string
	}{
		{"a receiver that broadcasts On", cectest.AudioBroadcast, ConditionTrue, reasonConfirmed, 1,
			"the adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and the receiver broadcast Set System Audio Mode On after <time>"},
		{"a receiver that tells the TV alone", cectest.AudioToTV, ConditionTrue, reasonConfirmed, 2,
			"the adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after; the receiver answered Give System Audio Mode Status with On"},
		{"a TV that refuses the mode", cectest.AudioRefusedByTV, ConditionFalse, reasonUnconfirmed, 2,
			"the adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after; the receiver answered Give System Audio Mode Status with Off; a TV that does not take System Audio Mode refuses the receiver's On, and the receiver stays Off"},
		{"a receiver with HDMI control off", cectest.AudioSilent, ConditionFalse, reasonUnconfirmed, 2,
			"the adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after; the receiver did not answer Give System Audio Mode Status, which a receiver with its HDMI control off does not answer"},
		{"a receiver that refuses the request", cectest.AudioAbort, ConditionFalse, reasonRefused, 1,
			"the adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and the receiver answered with a Feature Abort after <time>"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := audioRoom(cec.SystemAudioOff, c.reply)

				_, television := wokeThrough(t, wire, throughReceiver)

				applied := conditionOf(television.Status.Conditions, conditionSystemAudioApplied)
				mustMatch(t, applied.Status, c.status)
				mustMatch(t, applied.Reason, c.reason)
				mustMatch(t, timeless([]string{applied.Message})[0], c.message)
				mustDeepEqual(t, framesOf(wire, cec.OpSystemAudioModeRequest), []string{"4->5 70 13 00"})
				mustMatch(t, len(framesOf(wire, cec.OpGiveSystemAudioModeState)), c.reads)
				mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonConfirmed)
			})
		})
	}
}

// The request goes out after the wake's claim and its guard, so the
// TV's own traffic as it comes on is over, and the receiver ends on the
// Display's input.
func TestTheRequestFollowsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast)

		wokeThrough(t, wire, throughReceiver)

		var order []string
		for _, message := range wire.Sent() {
			if opcode, _ := message.Opcode(); !message.IsPoll() && (opcode == cec.OpActiveSource || opcode == cec.OpSystemAudioModeRequest) {
				order = append(order, message.String())
			}
		}
		mustDeepEqual(t, order, []string{"4->f 82 13 00", "4->5 70 13 00"})
		receiver, _ := wire.Peer(5)
		mustMatch(t, receiver.AudioInput, cec.PhysicalAddress(0x1300))
		mustMatch(t, receiver.AudioMode, cec.SystemAudioOn)
	})
}

// A broadcast [On] that arrives before the request answers the TV's own
// request as it comes on, and does not count for this one, so the
// adapter still reads the mode when no [On] follows its request.
func TestAnOnBeforeTheRequestDoesNotCount(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOn, cectest.AudioToTV)
		session := wokeNow()
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		api.putTelevision(wakingThrough(session, throughReceiver))
		api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })

		wire.Send(cec.SetSystemAudioMode(5, cec.AddressBroadcast, cec.SystemAudioOn))
		television := wokeWith(t, api, session)

		mustMatch(t, len(framesOf(wire, cec.OpGiveSystemAudioModeState)), 2)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionSystemAudioApplied).Reason, reasonConfirmed)
	})
}

// A Display straight on the TV has no receiver on its path, so the wake
// sends no request and the condition is absent, and a wake with no
// receiver removes the condition of the wake before it.
func TestAWakeWithNoReceiverSendsNoRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast)
		api, first := wokeThrough(t, wire, throughReceiver)
		mustMatch(t, conditionOf(first.Status.Conditions, conditionSystemAudioApplied).Reason, reasonConfirmed)

		second := wokeAt(time.Now().Add(time.Second))
		api.putTelevision(wakingThrough(second, straightOn))
		television := wokeWith(t, api, second)

		mustMatch(t, len(framesOf(wire, cec.OpSystemAudioModeRequest)), 1)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionSystemAudioApplied).Type, "")
	})
}

// A guard that a person's choice of route ended leaves the input where
// the person put it, so the wake asks the receiver for nothing: the
// request would switch the receiver back to the Display.
func TestAWakeAPersonEndedSendsNoRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast)
		session := wokeNow()
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		api.putTelevision(wakingThrough(session, throughReceiver))
		api.waitUntil(t, "the first Active Source", func() bool { return len(claimsOf(wire)) == 1 })
		time.Sleep(cecBootGrace)

		wire.Send(cec.NewMessage(cec.AddressTV, cec.AddressBroadcast, cec.OpSetStreamPath, 0x20, 0x00))
		television := wokeWith(t, api, session)

		mustMatch(t, conditionOf(television.Status.Conditions, conditionWakeApplied).Reason, reasonChosen)
		mustMatch(t, len(framesOf(wire, cec.OpSystemAudioModeRequest)), 0)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionSystemAudioApplied).Type, "")
	})
}

// Each wake sends the request once: a pass, and a new node workload
// after the adapter comes back, send nothing more for a wake already
// run.
func TestTheRequestGoesOutOncePerWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast)
		api, _ := wokeThrough(t, wire, throughReceiver)

		api.nudge()
		time.Sleep(2 * cecWakeGuard)

		mustMatch(t, len(framesOf(wire, cec.OpSystemAudioModeRequest)), 1)
	})
}

// The condition's transition posts one Event, Normal for a mode the
// receiver confirmed and a Warning for one it refused, beside the
// wake's own.
func TestSystemAudioAppliedPostsItsEvent(t *testing.T) {
	cases := []struct {
		name  string
		reply cectest.AudioReply
		want  posted
	}{
		{"a confirmed mode", cectest.AudioBroadcast, posted{events.TypeNormal, reasonConfirmed, 1}},
		{"a refused request", cectest.AudioAbort, posted{events.TypeWarning, reasonRefused, 1}},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api, _ := wokeThrough(t, audioRoom(cec.SystemAudioOff, c.reply), throughReceiver)

				mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{
					{events.TypeNormal, reasonWaking, 1},
					{events.TypeNormal, reasonConfirmed, 1},
					c.want,
				})
			})
		})
	}
}

// A request that no device acknowledges is one the adapter could not
// send, such as when the receiver left the bus after the Deployment
// listed it as the via.
func TestARequestNoDeviceAcknowledgesIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := cectest.NewBus()
		wire.Add(televisionTV(cec.PowerOn))

		_, television := wokeThrough(t, wire, throughReceiver)

		applied := conditionOf(television.Status.Conditions, conditionSystemAudioApplied)
		mustMatch(t, applied.Reason, reasonRefused)
		mustMatch(t, applied.Message, "the adapter on node-1 could not send System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den: the wire answered NACK MAX_RETRIES")
	})
}

// A session that sleeps while the adapter waits for the receiver's
// answer ends the wait: the adapter reads nothing more, and the wake
// writes no SystemAudioApplied, because it did not learn the answer.
func TestASessionThatSleepsEndsTheWaitForTheReceiver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOff, cectest.AudioToTV)
		session := wokeNow()
		api := controlling(t, wire, lounge(""))
		api.scanned(t, "node-1")
		api.putTelevision(wakingThrough(session, throughReceiver))
		time.Sleep(cecWakeGuard)
		api.waitUntil(t, "the request", func() bool { return len(framesOf(wire, cec.OpSystemAudioModeRequest)) == 1 })

		asleep := *session
		asleep.Awake = false
		api.putTelevision(wakingThrough(&asleep, throughReceiver))
		television := wokeWith(t, api, session)

		mustMatch(t, len(framesOf(wire, cec.OpGiveSystemAudioModeState)), 1)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionSystemAudioApplied).Type, "")
	})
}
