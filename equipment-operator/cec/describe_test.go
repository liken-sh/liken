package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestDescribeDecodesEachVisibleMessage(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
		want    string
	}{
		{"active source", cec.ActiveSource(8, 0x1500), "Active Source 1.5.0.0"},
		{"inactive source", cec.NewMessage(8, 0, cec.OpInactiveSource, 0x15, 0x00), "Inactive Source 1.5.0.0"},
		{"request active source", cec.NewMessage(0, 15, cec.OpRequestActiveSource), "Request Active Source"},
		{"routing change", cec.NewMessage(0, 15, cec.OpRoutingChange, 0x12, 0x00, 0x15, 0x00), "Routing Change from 1.2.0.0 to 1.5.0.0"},
		{"routing information", cec.NewMessage(5, 15, cec.OpRoutingInformation, 0x15, 0x00), "Routing Information 1.5.0.0"},
		{"set stream path", cec.NewMessage(0, 15, cec.OpSetStreamPath, 0x15, 0x00), "Set Stream Path 1.5.0.0"},
		{"image view on", cec.ImageViewOn(8, 0), "Image View On"},
		{"text view on", cec.NewMessage(8, 0, cec.OpTextViewOn), "Text View On"},
		{"standby", cec.Standby(0, 15), "Standby"},
		{"system audio on", cec.NewMessage(5, 15, cec.OpSetSystemAudioMode, 1), "Set System Audio Mode On"},
		{"system audio off", cec.NewMessage(5, 15, cec.OpSetSystemAudioMode, 0), "Set System Audio Mode Off"},
		{"system audio undefined", cec.NewMessage(5, 15, cec.OpSetSystemAudioMode, 7), "Set System Audio Mode 0x07, which CEC does not define"},
		{"system audio request", cec.NewMessage(0, 5, cec.OpSystemAudioModeRequest, 0x15, 0x00), "System Audio Mode Request for 1.5.0.0"},
		{"system audio request for off", cec.NewMessage(0, 5, cec.OpSystemAudioModeRequest), "System Audio Mode Request with no physical address, which asks for Off"},
		{"power", cec.ReportPowerStatus(0, 8, cec.PowerToOn), "Report Power Status ToOn"},
		{"power undefined", cec.NewMessage(0, 8, cec.OpReportPowerStatus, 9), "Report Power Status 0x09, which CEC does not define"},
		{"a key", cec.NewMessage(0, 4, cec.OpUserControlPressed, 0x41), "User Control Pressed Volume Up"},
		{"a colored key", cec.NewMessage(0, 4, cec.OpUserControlPressed, 0x72), "User Control Pressed F2 (Red)"},
		{"a key cec.h does not list", cec.NewMessage(0, 4, cec.OpUserControlPressed, 0x7f), "User Control Pressed key 0x7f"},
		{"too few operands", cec.NewMessage(0, 15, cec.OpRoutingChange, 0x12, 0x00), "Routing Change with 2 operand bytes, too few to read: 12 00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, visible := cec.Describe(c.message)
			if !visible || got != c.want {
				t.Errorf("Describe(%v) = %q, %v; want %q", c.message, got, visible, c.want)
			}
		})
	}
}

func TestDescribeLeavesTrafficBetweenDevicesOut(t *testing.T) {
	cases := []struct {
		name    string
		message cec.Message
	}{
		{"poll", cec.Poll(4, 0)},
		{"osd name", cec.SetOSDName(0, 4, "TV")},
		{"vendor", cec.DeviceVendorID(0, 0x00e091)},
		{"power question", cec.GiveDevicePowerStatus(4, 0)},
		{"key release", cec.NewMessage(0, 4, cec.OpUserControlReleased)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, visible := cec.Describe(c.message); visible {
				t.Errorf("Describe(%v) = %q, want no words", c.message, got)
			}
		})
	}
}
