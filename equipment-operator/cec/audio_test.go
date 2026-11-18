package cec_test

// Tests of System Audio Control against the in-memory bus: the scan's
// read of the mode, ReadSystemAudioMode on its own, and the volume keys.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// busWithAudioSystem returns the test room plus a receiver that implements
// System Audio Control, starts in mode, and answers Give System Audio
// Mode Status the way reply sets.
func busWithAudioSystem(mode cec.SystemAudioMode, reply cectest.AudioReply) *cectest.Bus {
	bus := room()
	audio := receiver
	audio.SystemAudio, audio.AudioMode, audio.AudioReply = true, mode, reply
	bus.Add(audio)
	return bus
}

// The scan records the audio system's mode in the directory. A receiver
// of HDMI 1.3 or earlier replies with a Feature Abort, and one with HDMI
// control off does not reply, so for both the mode stays unknown.
func TestTheScanReadsTheSystemAudioMode(t *testing.T) {
	cases := []struct {
		name string
		bus  *cectest.Bus
		want cec.SystemAudioMode
	}{
		{"a receiver in the mode", busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast), cec.SystemAudioOn},
		{"a receiver out of the mode", busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast), cec.SystemAudioOff},
		{"a receiver with HDMI control off", busWithAudioSystem(cec.SystemAudioOn, cectest.AudioSilent), cec.SystemAudioUnknown},
		{"a receiver without the feature", room(), cec.SystemAudioUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := joined(t, c.bus)
			directory := cec.NewDirectory()
			directory.SetOwn(4)

			_, err := cec.Scan(device, directory, 4, nil)

			mustSucceed(t, err)
			if got := directory.Lookup(5).SystemAudioMode; got != c.want {
				t.Errorf("the directory holds the mode %q, want %q", got, c.want)
			}
		})
	}
}

// The scan sends the mode query only to the audio system, and sends it
// last among its queries to that device.
func TestTheScanAsksOnlyTheAudioSystemForTheMode(t *testing.T) {
	bus := busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast)
	_, device := joined(t, bus)

	_, err := cec.Scan(device, cec.NewDirectory(), 4, nil)

	mustSucceed(t, err)
	want := []string{"4->5 83", "4->5 46", "4->5 8c", "4->5 9f", "4->5 8f", "4->5 7d"}
	if sent := audioFrames(bus); !slices.Equal(sent, want) || modeQueries(bus) != 1 {
		t.Errorf("the scan sent %v to the receiver and %d mode queries, want %v and 1 mode query", sent, modeQueries(bus), want)
	}
}

// modeQueries counts the Give System Audio Mode Status messages sent on
// the bus.
func modeQueries(bus *cectest.Bus) int {
	count := 0
	for _, message := range bus.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpGiveSystemAudioModeState && !message.IsPoll() {
			count++
		}
	}
	return count
}

func TestReadSystemAudioModeReturnsTheModeOrUnknownWithTheResult(t *testing.T) {
	cases := []struct {
		name    string
		bus     *cectest.Bus
		want    cec.SystemAudioMode
		aborted bool
	}{
		{"an answer", busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast), cec.SystemAudioOn, false},
		{"no answer", busWithAudioSystem(cec.SystemAudioOn, cectest.AudioSilent), cec.SystemAudioUnknown, false},
		{"a Feature Abort", room(), cec.SystemAudioUnknown, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := joined(t, c.bus)
			directory := cec.NewDirectory()

			mode, result, err := cec.ReadSystemAudioMode(device, directory, 4)

			mustSucceed(t, err)
			if mode != c.want || result.Aborted != c.aborted || directory.Lookup(5).SystemAudioMode != c.want {
				t.Errorf("ReadSystemAudioMode returned %q (aborted %v) and the directory holds %q, want %q (aborted %v) in both",
					mode, result.Aborted, directory.Lookup(5).SystemAudioMode, c.want, c.aborted)
			}
		})
	}
}

// A request with the Display's physical address makes the receiver
// switch to that input and turn the mode on. The receiver then
// broadcasts [On], and the adapter receives it.
func TestARequestTurnsTheModeOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast)
		_, device := joined(t, bus)

		_, err := device.Transmit(cec.SystemAudioModeRequest(4, 0x1300), 0, 0)
		mustSucceed(t, err)
		synctest.Wait()

		heard, err := device.Receive(0)
		mustSucceed(t, err)
		peer, _ := bus.Peer(5)
		if heard.String() != "5->f 72 01" || peer.AudioInput != 0x1300 || peer.AudioMode != cec.SystemAudioOn {
			t.Errorf("the adapter heard %v and the receiver is on input %#x in the mode %q, want 5->f 72 01, input 0x1300, and the mode on", heard, peer.AudioInput, peer.AudioMode)
		}
	})
}

// Each volume key goes to the audio system as a press followed by a
// release. CEC Table 25 requires this of a device that sends System
// Audio Mode Request.
func TestAVolumeKeyIsAPressAndARelease(t *testing.T) {
	cases := []struct {
		name string
		key  cec.VolumeKey
		want []string
	}{
		{"volume up", cec.KeyVolumeUp, []string{"4->5 44 41", "4->5 45"}},
		{"volume down", cec.KeyVolumeDown, []string{"4->5 44 42", "4->5 45"}},
		{"mute", cec.KeyMute, []string{"4->5 44 43", "4->5 45"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast)
			_, device := joined(t, bus)

			result, err := cec.PressVolumeKey(device, 4, c.key)

			mustSucceed(t, err)
			mustSendExactly(t, bus, result, c.want)
		})
	}
}

// A press that no audio system acknowledges gets no release. A key code
// that is not a volume key returns an error before anything is sent.
func TestAVolumeKeyNeedsAnAudioSystem(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(television)
	_, device := joined(t, bus)

	result, err := cec.PressVolumeKey(device, 4, cec.KeyVolumeUp)

	mustSucceed(t, err)
	if !result.Nacked || len(audioFrames(bus)) != 1 {
		t.Errorf("the press returned %+v and the adapter sent %v to the audio system, want a Nack and 1 frame", result, audioFrames(bus))
	}
	if _, err := cec.PressVolumeKey(device, 4, 0x44); err == nil {
		t.Error("PressVolumeKey accepted 0x44 (Play), want an error")
	}
}

// mustSendExactly fails the test unless the press was acknowledged and
// the frames sent to the audio system equal want.
func mustSendExactly(t *testing.T, bus *cectest.Bus, result cec.Result, want []string) {
	t.Helper()
	if sent := audioFrames(bus); !result.Acked || !slices.Equal(sent, want) {
		t.Errorf("the press returned %+v and the adapter sent %v, want an Ack and %v", result, sent, want)
	}
}

// audioFrames returns the frames the adapter sent to the audio system,
// leaving out polls.
func audioFrames(bus *cectest.Bus) []string {
	var frames []string
	for _, message := range bus.Sent() {
		if message.To == 5 && !message.IsPoll() {
			frames = append(frames, message.String())
		}
	}
	return frames
}
