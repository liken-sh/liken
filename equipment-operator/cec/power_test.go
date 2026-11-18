package cec_test

import (
	"testing"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// tvPeer returns the directory's peer for the TV, and false when the
// directory has none.
func tvPeer(directory *cec.Directory) (cec.Peer, bool) {
	for _, peer := range directory.Peers() {
		if peer.Logical == cec.AddressTV {
			return peer, true
		}
	}
	return cec.Peer{}, false
}

func TestAPowerReadRecordsWhatTheTVAnswers(t *testing.T) {
	mute := television
	mute.Mute = true
	garbled := television
	garbled.Garbled = true
	cases := []struct {
		name    string
		tv      *cectest.Peer
		reads   int
		want    cec.PowerStatus
		present bool
		held    cec.PowerStatus
	}{
		{"a TV that answers", &television, 1, cec.PowerStandby, true, cec.PowerStandby},
		{"a TV that misses one reply", &mute, 1, cec.PowerUnknown, true, cec.PowerOn},
		{"a TV that misses two replies in a row", &mute, 2, cec.PowerUnknown, true, cec.PowerUnknown},
		{"no TV", nil, 1, cec.PowerUnknown, false, cec.PowerUnknown},
		{"a disturbed wire", &garbled, 2, cec.PowerUnknown, true, cec.PowerOn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := cectest.NewBus()
			if c.tv != nil {
				bus.Add(*c.tv)
			}
			_, device := joined(t, bus)
			directory := cec.NewDirectory()
			directory.SetOwn(4)
			directory.Observe(cec.ReportPowerStatus(0, 4, cec.PowerOn))

			var power cec.PowerStatus
			for range c.reads {
				var err error
				power, err = cec.ReadPower(device, directory, 4, cec.AddressTV)
				mustSucceed(t, err)
			}

			peer, present := tvPeer(directory)
			if power != c.want || present != c.present || (present && peer.Power != c.held) {
				t.Errorf("ReadPower = %v and the directory holds %+v (present %v), want %v, %+v (present %v)", power, peer, present, c.want, c.held, c.present)
			}
		})
	}
}

func TestAnAnswerResetsTheMissedReplies(t *testing.T) {
	bus := cectest.NewBus()
	mute := television
	mute.Mute = true
	bus.Add(mute)
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)
	_, err := cec.ReadPower(device, directory, 4, cec.AddressTV)
	mustSucceed(t, err)
	bus.Add(television)
	_, err = cec.ReadPower(device, directory, 4, cec.AddressTV)
	mustSucceed(t, err)
	bus.Add(mute)

	_, err = cec.ReadPower(device, directory, 4, cec.AddressTV)

	mustSucceed(t, err)
	peer, _ := tvPeer(directory)
	if peer.Power != cec.PowerStandby {
		t.Errorf("the directory holds %+v after the third read, want Power %v", peer, cec.PowerStandby)
	}
}

func TestAPowerReadStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()

	_, err := cec.ReadPower(device, cec.NewDirectory(), 4, cec.AddressTV)

	if !cec.IsGone(err) {
		t.Errorf("ReadPower returned %v, want an error that IsGone reports (ENODEV)", err)
	}
}

func TestThePowerCommandForEachState(t *testing.T) {
	cases := []struct {
		name    string
		on      bool
		message cec.Message
	}{
		{"on", true, cec.ImageViewOn(4, cec.AddressTV)},
		{"standby", false, cec.Standby(4, cec.AddressTV)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			message := cec.PowerCommand(4, c.on)

			if message.String() != c.message.String() {
				t.Errorf("PowerCommand(4, %v) = %v, want %v", c.on, message, c.message)
			}
		})
	}
}

// The fake TV reports the transition state on the first read after a
// power command, and the requested state on the next.
func TestTheTVObeysThePowerCommands(t *testing.T) {
	bus := cectest.NewBus()
	slow := television
	slow.Transition = 1
	bus.Add(slow)
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	_, err := device.Transmit(cec.PowerCommand(4, true), 0, 0)
	mustSucceed(t, err)
	first, err := cec.ReadPower(device, directory, 4, cec.AddressTV)
	mustSucceed(t, err)
	second, err := cec.ReadPower(device, directory, 4, cec.AddressTV)
	mustSucceed(t, err)

	if first != cec.PowerToOn || second != cec.PowerOn {
		t.Errorf("the reads after Image View On return %v and then %v, want %v and then %v", first, second, cec.PowerToOn, cec.PowerOn)
	}
}
