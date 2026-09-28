package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// tvPeer answers what the directory holds for the TV, and false when
// it holds nothing at address 0.
func tvPeer(directory *cec.Directory) (cec.Peer, bool) {
	for _, peer := range directory.Peers() {
		if peer.Logical == cec.AddressTV {
			return peer, true
		}
	}
	return cec.Peer{}, false
}

// A read of the TV's power records what the TV answered. A TV that
// acknowledges and does not answer twice in a row has no power the
// operator can state, so the second missed reply clears the power the
// directory held. One missed reply keeps it, because a TV that wakes
// can miss one question. A NACK means no device holds address 0. A
// lost arbitration says nothing about the TV, so the directory keeps
// what it held.
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
				t.Errorf("read %v; the directory holds %+v (present %v)", power, peer, present)
			}
		})
	}
}

// An answer between two missed replies starts the count again, so
// only two missed replies in a row clear the power.
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
		t.Errorf("the directory holds %+v", peer)
	}
}

func TestAPowerReadStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()

	_, err := cec.ReadPower(device, cec.NewDirectory(), 4, cec.AddressTV)

	if !cec.IsGone(err) {
		t.Errorf("read answered %v, want ENODEV", err)
	}
}

// The generic order: Image View On wakes the TV, and Standby to the TV
// alone puts it in standby and leaves the receiver and the other
// sources on.
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
				t.Errorf("got %v, want %v", message, c.message)
			}
		})
	}
}

// Against the bus in memory, the commands move the TV the way a TV
// moves: through the transition, and then to the state asked for.
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
		t.Errorf("read %v and then %v", first, second)
	}
}
