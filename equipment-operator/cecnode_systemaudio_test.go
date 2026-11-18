package main

// cecnode_wake_audio_test.go tests the request that the wake sends.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// audioReceiver is the room's receiver with System Audio Control.
func audioReceiver(mode cec.SystemAudioMode, reply cectest.AudioReply) cectest.Peer {
	return cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn,
		SystemAudio: true, AudioMode: mode, AudioReply: reply}
}

func busWithAudioSystem(mode cec.SystemAudioMode, reply cectest.AudioReply) *cectest.Bus {
	wire := busWithTV(televisionTV(cec.PowerOn))
	wire.Add(audioReceiver(mode, reply))
	return wire
}

func audioSystemOf(entry CECAdapterStatus) CECDevice {
	for _, device := range entry.Devices {
		if device.LogicalAddress == int(cec.AddressAudioSystem) {
			return device
		}
	}
	return CECDevice{}
}

// When the receiver does not reply, the entry has no mode.
func TestTheEntryHoldsTheModeTheScanRead(t *testing.T) {
	cases := []struct {
		name string
		wire func() *cectest.Bus
		mode string
	}{
		{"a receiver in the mode", func() *cectest.Bus { return busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast) }, "On"},
		{"a receiver out of the mode", func() *cectest.Bus { return busWithAudioSystem(cec.SystemAudioOff, cectest.AudioBroadcast) }, "Off"},
		{"a receiver without the feature", func() *cectest.Bus { return busWithTV(televisionTV(cec.PowerOn)) }, ""},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := controlling(t, c.wire(), lounge(""))

				device := audioSystemOf(api.scanned(t, "node-1"))

				mustMatch(t, device.SystemAudioMode, c.mode)
				mustMatch(t, device.SystemAudioModeAt != "", c.mode != "")
			})
		})
	}
}

// A receiver broadcasts Set System Audio Mode Off before standby, and
// On when a person picks it in the TV's speaker menu. The adapter sends
// nothing in reply.
func TestABroadcastSetsTheMode(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast)
		api := controlling(t, wire, lounge(""))
		scanned := api.scanned(t, "node-1")
		sent := len(wire.Sent())
		time.Sleep(time.Minute)

		wire.Send(cec.SetSystemAudioMode(5, cec.AddressBroadcast, cec.SystemAudioOff))

		entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
			return audioSystemOf(entry).SystemAudioMode == "Off"
		})
		if audioSystemOf(entry).SystemAudioModeAt == audioSystemOf(scanned).SystemAudioModeAt {
			t.Errorf("SystemAudioModeAt is %s after the broadcast, want a time later than %s", audioSystemOf(entry).SystemAudioModeAt, audioSystemOf(scanned).SystemAudioModeAt)
		}
		mustMatch(t, len(wire.Sent()), sent)
	})
}

// On a bus with no audio system, the adapter never sends System Audio
// Mode Request, so a directed Set System Audio Mode or System Audio
// Mode Status is not a reply to it, and it sends a Feature Abort.
func TestTheAdapterAbortsSystemAudioOnlyWithoutAnAudioSystem(t *testing.T) {
	alone := func() *cectest.Bus {
		wire := cectest.NewBus()
		wire.Add(televisionTV(cec.PowerOn))
		return wire
	}
	withReceiver := func() *cectest.Bus { return busWithAudioSystem(cec.SystemAudioOn, cectest.AudioBroadcast) }
	cases := []struct {
		name    string
		wire    func() *cectest.Bus
		message cec.Message
		aborts  []string
	}{
		{"a Set System Audio Mode on a bus with no audio system", alone, cec.SetSystemAudioMode(0, 4, cec.SystemAudioOn), []string{"4->0 00 72 00"}},
		{"a System Audio Mode Status on a bus with no audio system", alone, cec.SystemAudioModeStatus(0, 4, cec.SystemAudioOn), []string{"4->0 00 7e 00"}},
		{"a broadcast on a bus with no audio system", alone, cec.SetSystemAudioMode(0, cec.AddressBroadcast, cec.SystemAudioOn), nil},
		{"a Set System Audio Mode on a bus with an audio system", withReceiver, cec.SetSystemAudioMode(5, 4, cec.SystemAudioOn), nil},
	}
	t.Parallel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				wire := c.wire()
				api := controlling(t, wire, lounge(""))
				api.scanned(t, "node-1")

				wire.Send(c.message)
				synctest.Wait()

				mustDeepEqual(t, abortsOf(wire), c.aborts)
			})
		})
	}
}

func abortsOf(wire *cectest.Bus) []string {
	var aborts []string
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpFeatureAbort && !message.IsPoll() {
			aborts = append(aborts, message.String())
		}
	}
	return aborts
}
