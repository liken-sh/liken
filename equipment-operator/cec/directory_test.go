package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
)

// peerAfter folds one message into a directory for an adapter at
// logical address 4, and answers what the directory holds for the
// message's sender.
func peerAfter(t *testing.T, message cec.Message) (cec.Peer, bool) {
	t.Helper()
	directory := cec.NewDirectory()
	directory.SetOwn(4)
	directory.Observe(message)
	for _, peer := range directory.Peers() {
		if peer.Logical == message.From {
			return peer, true
		}
	}
	return cec.Peer{}, false
}

func TestTheDirectoryLearnsOneFactFromEachMessage(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
		check   func(cec.Peer) bool
	}{
		{"report physical address", cec.ReportPhysicalAddress(5, 0x1000, 5), func(p cec.Peer) bool {
			return p.Physical == 0x1000 && p.Type == cec.TypeAudioSystem
		}},
		{"a switch names its own type", cec.ReportPhysicalAddress(15-1, 0x1000, 6), func(p cec.Peer) bool {
			return p.Type == cec.TypeSwitch
		}},
		{"active source", cec.ActiveSource(8, 0x1500), func(p cec.Peer) bool {
			return p.Physical == 0x1500 && p.Type == cec.TypePlayback
		}},
		{"osd name", cec.SetOSDName(0, 4, "TV"), func(p cec.Peer) bool { return p.OSDName == "TV" }},
		{"vendor", cec.DeviceVendorID(0, 0x00e091), func(p cec.Peer) bool { return p.Vendor == 0x00e091 }},
		{"version", cec.CECVersionReport(0, 4, cec.Version14), func(p cec.Peer) bool { return p.Version == cec.Version14 }},
		{"power", cec.ReportPowerStatus(0, 4, cec.PowerStandby), func(p cec.Peer) bool { return p.Power == cec.PowerStandby }},
		{"any message makes its sender present", cec.GiveDevicePowerStatus(0, 5), func(p cec.Peer) bool {
			return p.Physical == cec.InvalidPhysicalAddress && p.Power == cec.PowerUnknown && p.Vendor == cec.VendorUnknown
		}},
		{"a short operand is ignored", cec.NewMessage(0, 15, cec.OpReportPhysicalAddr, 0x10), func(p cec.Peer) bool {
			return p.Physical == cec.InvalidPhysicalAddress
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			peer, held := peerAfter(t, c.message)
			if !held || !c.check(peer) {
				t.Errorf("after %v the directory holds %+v (held %v)", c.message, peer, held)
			}
		})
	}
}

func TestTheDirectoryIgnoresNoDevice(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
	}{
		{"the unregistered address", cec.ActiveSource(15, 0x1000)},
		{"the adapter's own address", cec.ActiveSource(4, 0x1000)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, held := peerAfter(t, c.message); held {
				t.Errorf("the directory holds a peer after %v", c.message)
			}
		})
	}
}

func TestObserveAnswersWhetherAnythingChanged(t *testing.T) {
	directory := cec.NewDirectory()
	report := cec.ReportPowerStatus(0, 4, cec.PowerOn)

	first := directory.Observe(report)
	again := directory.Observe(report)
	changed := directory.Observe(cec.ReportPowerStatus(0, 4, cec.PowerStandby))

	if !first || again || !changed {
		t.Errorf("first %v, again %v, changed %v; want true, false, true", first, again, changed)
	}
}

func TestPresentForgetAndResetKeepTheList(t *testing.T) {
	directory := cec.NewDirectory()
	directory.SetOwn(4)

	added := directory.Present(0)
	addedAgain := directory.Present(0)
	own := directory.Present(4)
	directory.Present(5)
	forgot := directory.Forget(5)
	forgotAgain := directory.Forget(5)

	if !added || addedAgain || own || !forgot || forgotAgain {
		t.Errorf("added %v %v, own %v, forgot %v %v", added, addedAgain, own, forgot, forgotAgain)
	}
	if peers := directory.Peers(); len(peers) != 1 || peers[0].Logical != 0 || peers[0].Type != cec.TypeTV {
		t.Errorf("peers = %+v, want the TV alone", peers)
	}
	directory.Reset()
	if peers := directory.Peers(); len(peers) != 0 {
		t.Errorf("peers after reset = %+v", peers)
	}
}

func TestSetOwnForgetsThePeerAtThatAddress(t *testing.T) {
	directory := cec.NewDirectory()
	directory.Present(8)

	directory.SetOwn(8)

	if peers := directory.Peers(); len(peers) != 0 {
		t.Errorf("peers = %+v, want none", peers)
	}
}

func TestPeersAreInLogicalOrder(t *testing.T) {
	directory := cec.NewDirectory()
	for _, address := range []cec.LogicalAddress{8, 0, 5} {
		directory.Present(address)
	}

	peers := directory.Peers()

	if len(peers) != 3 || peers[0].Logical != 0 || peers[1].Logical != 5 || peers[2].Logical != 8 {
		t.Errorf("peers = %+v", peers)
	}
}
