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

	report, err := cec.Scan(device, directory, 4, nil)

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

	_, err := cec.Scan(device, directory, 4, nil)

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

	_, err := cec.Scan(device, directory, 4, nil)

	mustSucceed(t, err)
	peers := directory.Peers()
	if len(peers) != 1 || peers[0].Power != cec.PowerUnknown || peers[0].OSDName != "" {
		t.Errorf("peers = %+v", peers)
	}
}

func TestAScanStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()

	_, err := cec.Scan(device, cec.NewDirectory(), 4, nil)

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
	_, err := cec.Scan(device, directory, 4, nil)
	mustSucceed(t, err)
	disturbed := television
	disturbed.Garbled = true
	bus.Add(disturbed)

	report, err := cec.Scan(device, directory, 4, nil)

	mustSucceed(t, err)
	peers := directory.Peers()
	if report.Acked != 1 || len(peers) != 2 || peers[0].OSDName != "TV" {
		t.Errorf("report %+v, peers %+v", report, peers)
	}
}

// A device that announces itself is asked only for the facts its
// announcement left out, and only once they are missing.
func TestAnIntroductionAsksOnlyForMissingFacts(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)
	directory.Observe(cec.ReportPhysicalAddress(5, 0x1000, 5))

	asked, err := cec.Introduce(device, directory, 4, 5, nil)
	mustSucceed(t, err)
	again, err := cec.Introduce(device, directory, 4, 5, nil)
	mustSucceed(t, err)

	want := cec.Peer{Logical: 5, Physical: 0x1000, Type: cec.TypeAudioSystem, OSDName: "AVR", Vendor: 0x0005cd, Version: cec.Version14, Power: cec.PowerOn}
	if asked != 4 || again != 0 || directory.Lookup(5) != want {
		t.Errorf("asked %d, then %d; the directory holds %+v", asked, again, directory.Lookup(5))
	}
	for _, message := range bus.Sent() {
		if message.To != 5 {
			t.Errorf("the introduction sent %v to another device", message)
		}
	}
}

func TestAnIntroductionForgetsADeviceThatLeft(t *testing.T) {
	bus := room()
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)
	directory.Present(9)

	asked, err := cec.Introduce(device, directory, 4, 9, nil)

	mustSucceed(t, err)
	if asked != 1 || len(directory.Peers()) != 0 {
		t.Errorf("asked %d; the directory holds %+v", asked, directory.Peers())
	}
}

func TestAnIntroductionSkipsNoDevice(t *testing.T) {
	_, device := joined(t, room())

	for _, address := range []cec.LogicalAddress{4, 15} {
		asked, err := cec.Introduce(device, cec.NewDirectory(), 4, address, nil)
		mustSucceed(t, err)
		if asked != 0 {
			t.Errorf("address %d: asked %d", address, asked)
		}
	}
}

func TestAnIntroductionStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()
	directory := cec.NewDirectory()
	directory.Present(5)

	_, err := cec.Introduce(device, directory, 4, 5, nil)

	if !cec.IsGone(err) {
		t.Errorf("introduce answered %v, want ENODEV", err)
	}
}

// A caller that serializes its reads of the TV's power hands the scan
// and the introduction a reader, and they ask the TV its power through
// it, once, and never with a question of their own.
func TestTheTVsPowerGoesThroughTheReader(t *testing.T) {
	cases := []struct {
		name string
		ask  func(device *cec.Device, directory *cec.Directory, read cec.PowerReader) error
	}{
		{"a scan", func(device *cec.Device, directory *cec.Directory, read cec.PowerReader) error {
			_, err := cec.Scan(device, directory, 4, read)
			return err
		}},
		{"an introduction", func(device *cec.Device, directory *cec.Directory, read cec.PowerReader) error {
			_, err := cec.Introduce(device, directory, 4, cec.AddressTV, read)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bus := room()
			_, device := joined(t, bus)
			directory := cec.NewDirectory()
			directory.SetOwn(4)
			reads := 0

			mustSucceed(t, c.ask(device, directory, func() error { reads++; return nil }))

			if reads != 1 {
				t.Errorf("the reader ran %d times", reads)
			}
			for _, message := range bus.Sent() {
				if opcode, _ := message.Opcode(); message.To == cec.AddressTV && opcode == cec.OpGiveDevicePowerStatus {
					t.Errorf("the %s sent %v itself", c.name, message)
				}
			}
		})
	}
}
