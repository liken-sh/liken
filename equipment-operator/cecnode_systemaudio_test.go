package main

// The System Audio Mode as the node workload reports it: the mode in
// the adapter's entry from the join scan and from each broadcast Set
// System Audio Mode, and the Feature Abort of the two System Audio
// answers on a bus with no audio system. cecnode_wake_audio_test.go
// holds the request in the wake.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// audioReceiver is the room's receiver as an audio system that
// implements System Audio Control, in a mode.
func audioReceiver(mode cec.SystemAudioMode, reply cectest.AudioReply) cectest.Peer {
	return cectest.Peer{Logical: 5, Physical: 0x1000, PrimaryType: 5, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn,
		SystemAudio: true, AudioMode: mode, AudioReply: reply}
}

// audioRoom is the room with a TV that is on and a receiver that holds
// a mode.
func audioRoom(mode cec.SystemAudioMode, reply cectest.AudioReply) *cectest.Bus {
	wire := roomWithTV(televisionTV(cec.PowerOn))
	wire.Add(audioReceiver(mode, reply))
	return wire
}

// audioSystemOf answers the entry's device at logical address 5.
func audioSystemOf(entry CECAdapterStatus) CECDevice {
	for _, device := range entry.Devices {
		if device.LogicalAddress == int(cec.AddressAudioSystem) {
			return device
		}
	}
	return CECDevice{}
}

// The join scan reads the mode once, and the entry holds it with the
// time the adapter read it. A receiver that does not answer leaves the
// mode absent.
func TestTheEntryHoldsTheModeTheScanRead(t *testing.T) {
	cases := []struct {
		name string
		wire func() *cectest.Bus
		mode string
	}{
		{"a receiver in the mode", func() *cectest.Bus { return audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast) }, "On"},
		{"a receiver out of the mode", func() *cectest.Bus { return audioRoom(cec.SystemAudioOff, cectest.AudioBroadcast) }, "Off"},
		{"a receiver without the feature", func() *cectest.Bus { return roomWithTV(televisionTV(cec.PowerOn)) }, ""},
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

// Each broadcast Set System Audio Mode sets the mode, such as the [Off]
// the receiver broadcasts before standby, or the [On] when a person
// picks the receiver in the TV's speaker menu. The adapter asks nothing
// for it.
func TestABroadcastSetsTheMode(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		wire := audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast)
		api := controlling(t, wire, lounge(""))
		scanned := api.scanned(t, "node-1")
		sent := len(wire.Sent())
		time.Sleep(time.Minute)

		wire.Send(cec.SetSystemAudioMode(5, cec.AddressBroadcast, cec.SystemAudioOff))

		entry := api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool {
			return audioSystemOf(entry).SystemAudioMode == "Off"
		})
		if audioSystemOf(entry).SystemAudioModeAt == audioSystemOf(scanned).SystemAudioModeAt {
			t.Errorf("the mode's time stayed at %s", audioSystemOf(entry).SystemAudioModeAt)
		}
		mustMatch(t, len(wire.Sent()), sent)
	})
}

// A follower on a bus with no audio system never sends System Audio
// Mode Request, so it answers a directed Set System Audio Mode or
// System Audio Mode Status with a Feature Abort. On a bus with an audio
// system it takes both, and a broadcast gets no answer on either bus.
func TestTheAdapterAbortsSystemAudioOnlyWithoutAnAudioSystem(t *testing.T) {
	alone := func() *cectest.Bus {
		wire := cectest.NewBus()
		wire.Add(televisionTV(cec.PowerOn))
		return wire
	}
	withReceiver := func() *cectest.Bus { return audioRoom(cec.SystemAudioOn, cectest.AudioBroadcast) }
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

// abortsOf answers the Feature Aborts the adapters sent, as the log
// writes them.
func abortsOf(wire *cectest.Bus) []string {
	var aborts []string
	for _, message := range wire.Sent() {
		if opcode, _ := message.Opcode(); opcode == cec.OpFeatureAbort && !message.IsPoll() {
			aborts = append(aborts, message.String())
		}
	}
	return aborts
}
