package cectest_test

// A scripted TV moves its power the way a TV moves it, so a test of a
// program that wakes the TV and reads the power back sees what it sees
// on a wire.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

// commanded sends one message from a playback adapter and answers the
// peer at a logical address afterwards.
func commanded(t *testing.T, peers []cectest.Peer, message cec.Message, address cec.LogicalAddress) cectest.Peer {
	t.Helper()
	bus := cectest.NewBus()
	for _, peer := range peers {
		bus.Add(peer)
	}
	_, device := playing(t, bus, 0x1300, "node-1")
	_, err := device.Transmit(message, 0, 0)
	mustSucceed(t, err)
	peer, held := bus.Peer(address)
	if !held {
		t.Fatalf("no peer at %d", address)
	}
	return peer
}

func TestAPeerObeysThePowerCommands(t *testing.T) {
	tv := cectest.Peer{Logical: 0, Power: cec.PowerStandby}
	on := cectest.Peer{Logical: 0, Power: cec.PowerOn}
	receiver := cectest.Peer{Logical: 5, Power: cec.PowerOn}
	ignoring := cectest.Peer{Logical: 0, Power: cec.PowerStandby, Ignore: 1}
	slow := cectest.Peer{Logical: 0, Power: cec.PowerStandby, Transition: 2}
	cases := []struct {
		name    string
		peers   []cectest.Peer
		message cec.Message
		address cec.LogicalAddress
		want    cec.PowerStatus
	}{
		{"Image View On wakes the TV", []cectest.Peer{tv}, cec.ImageViewOn(4, 0), 0, cec.PowerOn},
		{"Standby to the TV", []cectest.Peer{on, receiver}, cec.Standby(4, 0), 0, cec.PowerStandby},
		{"Standby to the TV leaves the receiver on", []cectest.Peer{on, receiver}, cec.Standby(4, 0), 5, cec.PowerOn},
		{"a broadcast Standby reaches every peer", []cectest.Peer{on, receiver}, cec.Standby(4, cec.AddressBroadcast), 5, cec.PowerStandby},
		{"Image View On to another device wakes nothing", []cectest.Peer{tv, {Logical: 5, Power: cec.PowerStandby}}, cec.ImageViewOn(4, 5), 5, cec.PowerStandby},
		{"an ignored command", []cectest.Peer{ignoring}, cec.ImageViewOn(4, 0), 0, cec.PowerStandby},
		{"a slow TV starts its transition", []cectest.Peer{slow}, cec.ImageViewOn(4, 0), 0, cec.PowerToOn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			peer := commanded(t, c.peers, c.message, c.address)

			if peer.Power != c.want {
				t.Errorf("the peer is at %v, want %v", peer.Power, c.want)
			}
		})
	}
}

// A TV acknowledges a power command and sends no Feature Abort, so a
// sender reads the result as a command the TV took.
func TestAPowerCommandIsNotAborted(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Power: cec.PowerStandby})
	_, device := playing(t, bus, 0x1300, "node-1")

	result, err := device.Transmit(cec.ImageViewOn(4, 0), 0, 0)

	mustSucceed(t, err)
	if !result.Acked || result.Aborted {
		t.Errorf("result %+v", result)
	}
}

// A TV in its transition answers the transition for as many power
// questions as its Transition states, and then the state it moved to.
func TestASlowTVReportsItsTransitionFirst(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Power: cec.PowerOn, Transition: 2})
	_, device := playing(t, bus, 0x1300, "node-1")
	_, err := device.Transmit(cec.Standby(4, 0), 0, 0)
	mustSucceed(t, err)

	var answers []cec.PowerStatus
	for range 3 {
		result, err := device.Transmit(cec.GiveDevicePowerStatus(4, 0), cec.OpReportPowerStatus, 0)
		mustSucceed(t, err)
		answers = append(answers, cec.PowerStatus(result.Reply.Operands()[0]))
	}

	want := []cec.PowerStatus{cec.PowerToStandby, cec.PowerToStandby, cec.PowerStandby}
	if len(answers) != 3 || answers[0] != want[0] || answers[1] != want[1] || answers[2] != want[2] {
		t.Errorf("answers %v, want %v", answers, want)
	}
}

// A TV can answer its old state for a while after a command changes
// it, as a real TV does for about 2 seconds. Lag is how many power
// questions it answers so.
func TestALaggingTVReportsItsOldStateFirst(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Power: cec.PowerStandby, Lag: 2})
	_, device := playing(t, bus, 0x1300, "node-1")
	_, err := device.Transmit(cec.ImageViewOn(4, 0), 0, 0)
	mustSucceed(t, err)

	var answers []cec.PowerStatus
	for range 3 {
		result, err := device.Transmit(cec.GiveDevicePowerStatus(4, 0), cec.OpReportPowerStatus, 0)
		mustSucceed(t, err)
		answers = append(answers, cec.PowerStatus(result.Reply.Operands()[0]))
	}

	want := []cec.PowerStatus{cec.PowerStandby, cec.PowerStandby, cec.PowerOn}
	if len(answers) != 3 || answers[0] != want[0] || answers[1] != want[1] || answers[2] != want[2] {
		t.Errorf("answers %v, want %v", answers, want)
	}
}
