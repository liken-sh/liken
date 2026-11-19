package cec_test

import (
	"testing"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
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
		t.Errorf("the scan acked %d devices and the directory holds %+v, want 2 acked and %+v", report.Acked, peers, want)
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
			t.Errorf("after the scan the directory still holds %+v, want no device at address 9", peer)
		}
	}
}

// A TV in deep standby acknowledges its poll and replies to nothing
// else, so the directory has the TV with no facts.
func TestAMuteDeviceIsPresentWithNoFacts(t *testing.T) {
	bus := cectest.NewBus()
	bus.Add(cectest.Peer{Logical: 0, Mute: true})
	_, device := joined(t, bus)
	directory := cec.NewDirectory()

	_, err := cec.Scan(device, directory, 4, nil)

	mustSucceed(t, err)
	peers := directory.Peers()
	if len(peers) != 1 || peers[0].Power != cec.PowerUnknown || peers[0].OSDName != "" {
		t.Errorf("the directory holds %+v, want one device with unknown power and no OSD name", peers)
	}
}

func TestAScanStopsWhenTheAdapterLeaves(t *testing.T) {
	adapter, device := joined(t, room())
	adapter.Unplug()

	_, err := cec.Scan(device, cec.NewDirectory(), 4, nil)

	if !cec.IsGone(err) {
		t.Errorf("Scan returned %v, want an error that IsGone reports (ENODEV)", err)
	}
}

// A poll that hits a wire error is not a NACK, so the scan keeps what
// the earlier scan recorded for that device.
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
		t.Errorf("the second scan acked %d devices and the directory holds %+v, want 1 acked and both devices with the TV's OSD name kept", report.Acked, peers)
	}
}

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
		t.Errorf("the first introduction asked %d questions, the second asked %d, and the directory holds %+v, want 4, 0, and %+v", asked, again, directory.Lookup(5), want)
	}
	for _, message := range bus.Sent() {
		if message.To != 5 {
			t.Errorf("the introduction of address 5 sent %v to another device, want every message to address 5", message)
		}
	}
}

// A device that replies to Give Device Vendor ID with 000000 has no
// vendor. The directory records that, so the next introduction does not
// ask again, and Vendor stays VendorUnknown.
func TestAVendorOfZeroIsAskedOnce(t *testing.T) {
	tv := television
	tv.Vendor = 0
	bus := cectest.NewBus()
	bus.Add(tv)
	bus.Add(receiver)
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)

	_, err := cec.Introduce(device, directory, 4, 0, nil)
	mustSucceed(t, err)
	again, err := cec.Introduce(device, directory, 4, 0, nil)
	mustSucceed(t, err)

	if again != 0 || directory.Lookup(0).Vendor != cec.VendorUnknown {
		t.Errorf("the second introduction asked %d questions and the directory holds %+v, want 0 questions and VendorUnknown", again, directory.Lookup(0))
	}
}

// The CEC Version operand 0x00 is version 1.1 (HDMI-CEC 1.3a, CEC Table
// 26). A device that reports it has a known version, so
// the next introduction does not ask again, and the status names 1.1.
func TestAVersionOfZeroIsVersion11(t *testing.T) {
	tv := television
	tv.Version = cec.Version11
	bus := cectest.NewBus()
	bus.Add(tv)
	bus.Add(receiver)
	_, device := joined(t, bus)
	directory := cec.NewDirectory()
	directory.SetOwn(4)

	_, err := cec.Introduce(device, directory, 4, 0, nil)
	mustSucceed(t, err)
	again, err := cec.Introduce(device, directory, 4, 0, nil)
	mustSucceed(t, err)

	if got := directory.Lookup(0).Version.String(); again != 0 || got != "1.1" {
		t.Errorf("the second introduction asked %d questions and the directory holds version %q, want 0 questions and version 1.1", again, got)
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
		t.Errorf("the introduction asked %d questions and the directory holds %+v, want 1 question and no device", asked, directory.Peers())
	}
}

func TestAnIntroductionSkipsNoDevice(t *testing.T) {
	_, device := joined(t, room())

	for _, address := range []cec.LogicalAddress{4, 15} {
		asked, err := cec.Introduce(device, cec.NewDirectory(), 4, address, nil)
		mustSucceed(t, err)
		if asked != 0 {
			t.Errorf("the introduction of address %d asked %d questions, want 0", address, asked)
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
		t.Errorf("Introduce returned %v, want an error that IsGone reports (ENODEV)", err)
	}
}

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
				t.Errorf("the power reader ran %d times, want 1", reads)
			}
			for _, message := range bus.Sent() {
				if opcode, _ := message.Opcode(); message.To == cec.AddressTV && opcode == cec.OpGiveDevicePowerStatus {
					t.Errorf("%s sent %v to the TV, want the power reader to ask instead", c.name, message)
				}
			}
		})
	}
}
