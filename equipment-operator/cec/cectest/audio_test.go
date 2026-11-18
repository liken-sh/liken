package cectest_test

// The scripted audio system answers System Audio Mode Request as
// HDMI-CEC 1.3a, section CEC 13.15.2, describes. It also fails the
// request in the ways that a real receiver and TV fail it, so a test of
// the wake can cover each reply.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// queuedMessages returns, as text, every message queued for follower.
func queuedMessages(follower *cec.Device) []string {
	var messages []string
	for {
		message, err := follower.Receive(0)
		if err != nil {
			return messages
		}
		messages = append(messages, message.String())
	}
}

// audioSystem returns a receiver at logical address 5 that implements
// System Audio Control and starts with the mode off.
func audioSystem(reply cectest.AudioReply) cectest.Peer {
	return cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, Power: cec.PowerStandby,
		SystemAudio: true, AudioMode: cec.SystemAudioOff, AudioReply: reply, AudioAfter: time.Millisecond}
}

func TestAnAudioSystemAnswersARequest(t *testing.T) {
	cases := []struct {
		name    string
		reply   cectest.AudioReply
		request cec.Message
		heard   []string
		mode    cec.SystemAudioMode
		input   cec.PhysicalAddress
		power   cec.PowerStatus
	}{
		{"a request it broadcasts On for", cectest.AudioBroadcast, cec.SystemAudioModeRequest(4, 0x1300),
			[]string{"5->f 72 01"}, cec.SystemAudioOn, 0x1300, cec.PowerOn},
		{"a request it tells the TV alone", cectest.AudioToTV, cec.SystemAudioModeRequest(4, 0x1300),
			nil, cec.SystemAudioOn, 0x1300, cec.PowerOn},
		{"a request the TV refuses", cectest.AudioRefusedByTV, cec.SystemAudioModeRequest(4, 0x1300),
			nil, cec.SystemAudioOff, 0x1300, cec.PowerOn},
		{"a request with its HDMI control off", cectest.AudioSilent, cec.SystemAudioModeRequest(4, 0x1300),
			nil, cec.SystemAudioOff, 0, cec.PowerStandby},
		{"a request it refuses", cectest.AudioAbort, cec.SystemAudioModeRequest(4, 0x1300),
			[]string{"5->4 00 70 00"}, cec.SystemAudioOff, 0, cec.PowerStandby},
		{"a request with no operand", cectest.AudioBroadcast, cec.NewMessage(4, 5, cec.OpSystemAudioModeRequest),
			[]string{"5->f 72 00"}, cec.SystemAudioOff, 0, cec.PowerStandby},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bus := cectest.NewBus()
				bus.Add(audioSystem(c.reply))
				_, asker := playing(t, bus, 0x1300, "node-1")

				_, err := asker.Transmit(c.request, 0, 0)
				mustSucceed(t, err)
				time.Sleep(10 * time.Millisecond)

				peer, _ := bus.Peer(5)
				if got := queuedMessages(asker); !slices.Equal(got, c.heard) || peer.AudioMode != c.mode || peer.AudioInput != c.input || peer.Power != c.power {
					t.Errorf("the asker heard %v and the peer is in %q on %s at %s, want %v and %q on %s at %s",
						got, peer.AudioMode, peer.AudioInput, peer.Power, c.heard, c.mode, c.input, c.power)
				}
			})
		})
	}
}

// The audio system answers Give System Audio Mode Status with its mode,
// accepts volume keys without a reply, and answers nothing when its
// HDMI control is off. A peer without the feature aborts each of them.
func TestAnAudioSystemAnswersTheModeAndTakesTheKeys(t *testing.T) {
	cases := []struct {
		name    string
		peer    cectest.Peer
		request cec.Message
		reply   cec.Opcode
		answer  string
		aborted bool
	}{
		{"the mode", audioSystem(cectest.AudioBroadcast), cec.GiveSystemAudioModeStatus(4), cec.OpSystemAudioModeStatus, "5->4 7e 00", false},
		{"the mode with HDMI control off", audioSystem(cectest.AudioSilent), cec.GiveSystemAudioModeStatus(4), cec.OpSystemAudioModeStatus, "", false},
		{"a volume key", audioSystem(cectest.AudioBroadcast), cec.NewMessage(4, 5, cec.OpUserControlPressed, 0x41), 0, "", false},
		{"a key it has no use for", audioSystem(cectest.AudioBroadcast), cec.NewMessage(4, 5, cec.OpGiveAudioStatus), cec.OpReportAudioStatus, "", true},
		{"the mode from a device without the feature", cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5}, cec.GiveSystemAudioModeStatus(4), cec.OpSystemAudioModeStatus, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := cectest.NewBus()
			bus.Add(c.peer)
			_, asker := playing(t, bus, 0x1300, "node-1")

			result, err := asker.Transmit(c.request, c.reply, time.Second)

			mustSucceed(t, err)
			if answer := replyText(result); answer != c.answer || result.Aborted != c.aborted || !result.Acked {
				t.Errorf("the result is %+v with answer %q, want answer %q, aborted %v, and acked", result, replyText(result), c.answer, c.aborted)
			}
		})
	}
}

// replyText returns a transmit's reply in the form the log uses, or an
// empty string when there is no reply.
func replyText(result cec.Result) string {
	if result.Reply == nil {
		return ""
	}
	return result.Reply.String()
}
