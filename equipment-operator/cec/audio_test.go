package cec_test

// System Audio Control against the bus in memory: the scan's read of
// the mode, the read on its own, and the volume keys.

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// audioRoom is the room with a receiver that implements System Audio
// Control and holds a mode.
func audioRoom(mode cec.SystemAudioMode, reply cectest.AudioReply) *cectest.Bus {
	bus := room()
	audio := receiver
	audio.SystemAudio, audio.AudioMode, audio.AudioReply = true, mode, reply
	bus.Add(audio)
	return bus
}

// The scan asks the audio system for the mode after its other
// questions to that device, and the directory holds the answer. A
// receiver of HDMI 1.3 or earlier answers with a Feature Abort, and one
// with HDMI control off answers nothing, so the mode stays unknown.
func TestTheScanReadsTheSystemAudioMode(t *testing.T) {
	cases := []struct {
		name string
		bus  *cectest.Bus
		want cec.SystemAudioMode
	}{
		{"a receiver in the mode", audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast), cec.SystemAudioOn},
		{"a receiver out of the mode", audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast), cec.SystemAudioOff},
		{"a receiver with HDMI control off", audioRoom(cec.SystemAudioOn, cectest.AudioSilent), cec.SystemAudioUnknown},
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

// The question goes to the audio system alone, last of the questions
// to it.
func TestTheScanAsksOnlyTheAudioSystemForTheMode(t *testing.T) {
	bus := audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast)
	_, device := joined(t, bus)

	_, err := cec.Scan(device, cec.NewDirectory(), 4, nil)

	mustSucceed(t, err)
	want := []string{"4->5 83", "4->5 46", "4->5 8c", "4->5 9f", "4->5 8f", "4->5 7d"}
	if sent := audioFrames(bus); !slices.Equal(sent, want) || modeQuestions(bus) != 1 {
		t.Errorf("asked the receiver %v, and asked %d devices for the mode; want %v", sent, modeQuestions(bus), want)
	}
}

// modeQuestions counts the Give System Audio Mode Status on the wire.
func modeQuestions(bus *cectest.Bus) int {
	count := 0
	for _, message := range bus.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpGiveSystemAudioModeState && !message.IsPoll() {
			count++
		}
	}
	return count
}

func TestReadSystemAudioModeSaysWhyItKnowsNothing(t *testing.T) {
	cases := []struct {
		name    string
		bus     *cectest.Bus
		want    cec.SystemAudioMode
		aborted bool
	}{
		{"an answer", audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast), cec.SystemAudioOn, false},
		{"no answer", audioRoom(cec.SystemAudioOn, cectest.AudioSilent), cec.SystemAudioUnknown, false},
		{"a Feature Abort", room(), cec.SystemAudioUnknown, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, device := joined(t, c.bus)
			directory := cec.NewDirectory()

			mode, result, err := cec.ReadSystemAudioMode(device, directory, 4)

			mustSucceed(t, err)
			if mode != c.want || result.Aborted != c.aborted || directory.Lookup(5).SystemAudioMode != c.want {
				t.Errorf("read %q (aborted %v), the directory holds %q; want %q (aborted %v)",
					mode, result.Aborted, directory.Lookup(5).SystemAudioMode, c.want, c.aborted)
			}
		})
	}
}

// A request with the Display's physical address switches the receiver
// to that input and turns the mode on, and the receiver broadcasts
// [On], which the adapter hears.
func TestARequestTurnsTheModeOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast)
		_, device := joined(t, bus)

		_, err := device.Transmit(cec.SystemAudioModeRequest(4, 0x1300), 0, 0)
		mustSucceed(t, err)
		synctest.Wait()

		heard, err := device.Receive(0)
		mustSucceed(t, err)
		peer, _ := bus.Peer(5)
		if heard.String() != "5->f 72 01" || peer.AudioInput != 0x1300 || peer.AudioMode != cec.SystemAudioOn {
			t.Errorf("heard %v; the receiver is on input %s in the mode %q", heard, peer.AudioInput, peer.AudioMode)
		}
	})
}

// Each volume key is a press and a release to the audio system, the
// capability CEC Table 25 asks of a device that sends System Audio Mode
// Request.
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
			bus := audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast)
			_, device := joined(t, bus)

			result, err := cec.PressVolumeKey(device, 4, c.key)

			mustSucceed(t, err)
			mustSendExactly(t, bus, result, c.want)
		})
	}
}

// A press that no audio system acknowledges gets no release, and a key
// that is no volume key is refused before anything goes on the wire.
func TestAVolumeKeyNeedsAnAudioSystem(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(television)
	_, device := joined(t, bus)

	result, err := cec.PressVolumeKey(device, 4, cec.KeyVolumeUp)

	mustSucceed(t, err)
	if !result.Nacked || len(audioFrames(bus)) != 1 {
		t.Errorf("result %+v, frames %v", result, audioFrames(bus))
	}
	if _, err := cec.PressVolumeKey(device, 4, 0x44); err == nil {
		t.Error("pressed Play as a volume key")
	}
}

// mustSendExactly fails the test unless the press was acknowledged and
// the frames to the audio system are want.
func mustSendExactly(t *testing.T, bus *cectest.Bus, result cec.Result, want []string) {
	t.Helper()
	if sent := audioFrames(bus); !result.Acked || !slices.Equal(sent, want) {
		t.Errorf("result %+v, sent %v, want %v", result, sent, want)
	}
}

// audioFrames are the frames the adapter sent to the audio system that
// are not polls.
func audioFrames(bus *cectest.Bus) []string {
	var frames []string
	for _, message := range bus.Sent() {
		if message.To == 5 && !message.IsPoll() {
			frames = append(frames, message.String())
		}
	}
	return frames
}
