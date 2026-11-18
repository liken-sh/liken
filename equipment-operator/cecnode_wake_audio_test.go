package main

// Tests of the System Audio Mode Request that a wake sends to the
// receiver between the TV and the session's Display. The wake fixtures
// are in cecnode_wake_test.go, and the fake receiver is in
// cecnode_systemaudio_test.go.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
	"github.com/liken-sh/liken/kubernetes/events"
)

// throughReceiver is the session's Display, connected through the Receiver
// named den.
var throughReceiver = []TelevisionDisplay{
	{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0", Via: &EquipmentRef{Kind: "Receiver", Name: "den"}},
}

// straightOn is the session's Display, connected directly to the TV.
var straightOn = []TelevisionDisplay{{Name: "acm-0001-receiver", PhysicalAddress: "1.3.0.0"}}

func wakingThrough(session *TelevisionSession, displays []TelevisionDisplay) Television {
	television := waking(session)
	television.Status.Displays = displays
	return television
}

// wokeThrough runs the node workload on the wire, wakes the room with
// the given Displays, and waits for the wake to finish.
func wokeThrough(t *testing.T, wire *cectest.Bus, displays []TelevisionDisplay) (*cecAPI, Television) {
	t.Helper()
	session := wokeNow()
	api := controlling(t, wire, lounge(""))
	api.scanned(t, "node-1")
	api.putTelevision(wakingThrough(session, displays))
	return api, wokeWith(t, api, session)
}

// framesOf returns the messages with the given opcode that the adapters
// sent, as strings in the format of the log. Polls are excluded.
func framesOf(wire *cectest.Bus, opcode cec.Opcode) []string {
	var frames []string
	for _, message := range wire.Sent() {
		if sent, _ := message.Opcode(); sent == opcode && !message.IsPoll() {
			frames = append(frames, message.String())
		}
	}
	return frames
}

// The request has the Display's physical address, so the receiver
// switches to the Display's input. A broadcast Set System Audio Mode On
// after the request confirms it. When none arrives within
// cecSystemAudioWait, the adapter sends Give System Audio Mode Status
// again, after the one that the scan sent, and the condition follows
// that reply.
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
			"The adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and the receiver broadcast Set System Audio Mode On after <time>"},
		{"a receiver that tells the TV alone", cectest.AudioToTV, ConditionTrue, reasonConfirmed, 2,
			"The adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after. The receiver answered Give System Audio Mode Status with On"},
		{"a TV that refuses the mode", cectest.AudioRefusedByTV, ConditionFalse, reasonUnconfirmed, 2,
			"The adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after. The receiver answered Give System Audio Mode Status with Off. A TV that does not take System Audio Mode refuses the receiver's On, and the receiver stays Off"},
		{"a receiver with HDMI control off", cectest.AudioSilent, ConditionFalse, reasonUnconfirmed, 2,
			"The adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and heard no broadcast Set System Audio Mode On in the 5.0 s after. The receiver did not answer Give System Audio Mode Status, which a receiver with its HDMI control off does not answer"},
		{"a receiver that refuses the request", cectest.AudioAbort, ConditionFalse, reasonRefused, 1,
			"The adapter on node-1 sent System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den, and the receiver answered with a Feature Abort after <time>"},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := busWithAudioSystem(cec.SystemAudioOff, c.reply)

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

// The request goes out after the wake's Active Source and the wake
// guard. By then the TV has stopped sending its own startup messages,
// so the receiver stays on the Display's input.
func TestTheRequestFollowsTheGuard(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast)

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

// A broadcast Set System Audio Mode On before the request is the
// receiver's reply to the TV's own request as it wakes. It does not
// confirm the adapter's request, so the adapter still reads the mode.
func TestAnOnBeforeTheRequestDoesNotCount(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOn, cectest.AudioToTV)
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

// A wake with no receiver between the TV and the Display sends no
// request, and removes the condition that the previous wake wrote.
func TestAWakeWithNoReceiverSendsNoRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast)
		api, first := wokeThrough(t, wire, throughReceiver)
		mustMatch(t, conditionOf(first.Status.Conditions, conditionSystemAudioApplied).Reason, reasonConfirmed)

		second := wokeAt(time.Now().Add(time.Second))
		api.putTelevision(wakingThrough(second, straightOn))
		television := wokeWith(t, api, second)

		mustMatch(t, len(framesOf(wire, cec.OpSystemAudioModeRequest)), 1)
		mustMatch(t, conditionOf(television.Status.Conditions, conditionSystemAudioApplied).Type, "")
	})
}

// When a person switches the TV to another input during the wake guard,
// the wake sends no request, because the request would switch the
// receiver back to the Display.
func TestAWakeAPersonEndedSendsNoRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast)
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

func TestTheRequestGoesOutOncePerWake(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast)
		api, _ := wokeThrough(t, wire, throughReceiver)

		api.nudge()
		time.Sleep(2 * cecWakeGuard)

		mustMatch(t, len(framesOf(wire, cec.OpSystemAudioModeRequest)), 1)
	})
}

// Each change of SystemAudioApplied posts one Event after the wake's
// own Events: Normal when the receiver confirmed the mode, and Warning
// when it refused.
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
				api, _ := wokeThrough(t, busWithAudioSystem(cec.SystemAudioOff, c.reply), throughReceiver)

				mustDeepEqual(t, postedAbout(api.events, "Television", "lounge"), []posted{
					{events.TypeNormal, reasonWaking, 1},
					{events.TypeNormal, reasonConfirmed, 1},
					c.want,
				})
			})
		})
	}
}

// A NACK on the request means the receiver in the Display's via has
// left the bus.
func TestARequestNoDeviceAcknowledgesIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := cectest.NewBus()
		wire.Add(televisionTV(cec.PowerOn))

		_, television := wokeThrough(t, wire, throughReceiver)

		applied := conditionOf(television.Status.Conditions, conditionSystemAudioApplied)
		mustMatch(t, applied.Reason, reasonRefused)
		mustMatch(t, applied.Message, "The adapter on node-1 did not send System Audio Mode Request for 1.3.0.0, Display acm-0001-receiver, to Receiver den: the wire answered NACK MAX_RETRIES")
	})
}

// When the session goes to sleep while the adapter waits for the
// receiver's reply, the adapter stops waiting, sends no second read, and
// writes no SystemAudioApplied condition.
func TestASessionThatSleepsEndsTheWaitForTheReceiver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioToTV)
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
