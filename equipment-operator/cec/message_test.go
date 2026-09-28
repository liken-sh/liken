package cec_test

import (
	"bytes"
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestTheBuildersWriteTheOpcodeAndOperands(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
		to      cec.LogicalAddress
		body    []byte
	}{
		{"poll", cec.Poll(4, 0), 0, nil},
		{"give physical address", cec.GivePhysicalAddress(4, 0), 0, []byte{0x83}},
		{"give osd name", cec.GiveOSDName(4, 0), 0, []byte{0x46}},
		{"give vendor", cec.GiveDeviceVendorID(4, 0), 0, []byte{0x8c}},
		{"get cec version", cec.GetCECVersion(4, 0), 0, []byte{0x9f}},
		{"give power", cec.GiveDevicePowerStatus(4, 0), 0, []byte{0x8f}},
		{"report power", cec.ReportPowerStatus(4, 0, cec.PowerOn), 0, []byte{0x90, 0x00}},
		{"feature abort", cec.FeatureAbort(4, 0, cec.OpMenuRequest, cec.AbortUnrecognizedOpcode), 0, []byte{0x00, 0x8d, 0x00}},
		{"report physical address", cec.ReportPhysicalAddress(4, 0x1300, 4), 15, []byte{0x84, 0x13, 0x00, 0x04}},
		{"set osd name", cec.SetOSDName(4, 0, "node-1"), 0, []byte{0x47, 'n', 'o', 'd', 'e', '-', '1'}},
		{"vendor", cec.DeviceVendorID(0, 0x00e091), 15, []byte{0x87, 0x00, 0xe0, 0x91}},
		{"cec version", cec.CECVersionReport(0, 4, cec.Version14), 4, []byte{0x9e, 0x05}},
		{"active source", cec.ActiveSource(4, 0x1300), 15, []byte{0x82, 0x13, 0x00}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.message.To != c.to || !bytes.Equal(c.message.Body, c.body) {
				t.Errorf("got to %d body % x, want to %d body % x", c.message.To, c.message.Body, c.to, c.body)
			}
		})
	}
}

func TestAMessageReadsItsParts(t *testing.T) {
	poll := cec.Poll(4, 0)
	if _, has := poll.Opcode(); has || !poll.IsPoll() || poll.Operands() != nil {
		t.Errorf("a poll reads as a message with an opcode: %v", poll)
	}
	active := cec.ActiveSource(4, 0x1300)
	opcode, has := active.Opcode()
	if !has || opcode != cec.OpActiveSource || !active.IsBroadcast() || !bytes.Equal(active.Operands(), []byte{0x13, 0x00}) {
		t.Errorf("active source read wrong: %v", active)
	}
	if got := active.String(); got != "4->f 82 13 00" {
		t.Errorf("String = %q", got)
	}
}

func TestTheOperandsReadTheWayTheStatusWritesThem(t *testing.T) {
	cases := []struct {
		got  string
		want string
	}{
		{cec.PowerOn.String(), "On"},
		{cec.PowerStandby.String(), "Standby"},
		{cec.PowerToOn.String(), "ToOn"},
		{cec.PowerToStandby.String(), "ToStandby"},
		{cec.PowerUnknown.String(), ""},
		{cec.Version13a.String(), "1.3a"},
		{cec.Version14.String(), "1.4"},
		{cec.Version20.String(), "2.0"},
		{cec.VersionUnknown.String(), ""},
		{cec.VendorID(0x00e091).String(), "00e091"},
		{cec.VendorUnknown.String(), ""},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}
