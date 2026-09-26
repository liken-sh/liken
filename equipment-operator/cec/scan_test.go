package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/equipment-operator/cec/cectest"
)

func TestAScanFindsEveryDeviceAndItsFacts(t *testing.T) {
	_, device := joined(t, room())
	directory := cec.NewDirectory()
	directory.SetOwn(4)

	report, err := cec.Scan(device, directory, 4)

	mustSucceed(t, err)
	want := []cec.Peer{
		{Logical: 0, Physical: 0x0000, Type: cec.TypeTV, OSDName: "TV", Vendor: 0x00e091, Version: cec.Version14, Power: cec.PowerStandby},
		{Logical: 5, Physical: 0x1000, Type: cec.TypeAudioSystem, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn},
	}
	peers := directory.Peers()
	if report.Acked != 2 || len(peers) != 2 || peers[0] != want[0] || peers[1] != want[1] {
		t.Errorf("report %+v, peers %+v", report, peers)
	}
}

func TestAScanForgetsADeviceThatLeft(t *testing.T) {
	_, device := joined(t, room())
	directory := cec.NewDirectory()
	directory.Present(9)

	_, err := cec.Scan(device, directory, 4)

	mustSucceed(t, err)
	for _, peer := range directory.Peers() {
		if peer.Logical == 9 {
			t.Errorf("the directory still holds %+v", peer)
		}
	}
}

// A TV in deep standby acknowledges its poll and answers nothing, so it
// is present with no facts.
func TestAMuteDeviceIsPresentWithNoFacts(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Mute: true})
	_, device := joined(t, bus)
	directory := cec.NewDirectory()

	_, err := cec.Scan(device, directory, 4)

	mustSucceed(t, err)
	peers := directory.Peers()
	if len(peers) != 1 || peers[0].Power != cec.PowerUnknown || peers[0].OSDName != "" {
		t.Errorf("peers = %+v", peers)
	}
}

func TestAScanStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()

	_, err := cec.Scan(device, cec.NewDirectory(), 4)

	if !cec.IsGone(err) {
		t.Errorf("scan answered %v, want ENODEV", err)
	}
}

// Only a NACK means that no device holds an address. A poll that loses
// arbitration or meets a wire error says nothing about the device, so
// the scan keeps what the last scan found there.
func TestAScanKeepsADeviceItCouldNotReach(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)
	_, err := cec.Scan(device, directory, 4)
	mustSucceed(t, err)
	disturbed := television
	disturbed.Garbled = true
	bus.Add(disturbed)

	report, err := cec.Scan(device, directory, 4)

	mustSucceed(t, err)
	peers := directory.Peers()
	if report.Acked != 1 || len(peers) != 2 || peers[0].OSDName != "TV" {
		t.Errorf("report %+v, peers %+v", report, peers)
	}
}
