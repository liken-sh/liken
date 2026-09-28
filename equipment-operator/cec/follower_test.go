package cec_test

import (
	"testing"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestAFollowerAnswersWhatTheKernelLeavesToIt(t *testing.T) {
	const own = cec.LogicalAddress(4)
	cases := []struct {
		name    string
		message cec.Message
		want    *cec.Message
	}{
		{"power status", cec.GiveDevicePowerStatus(0, own), ptr(cec.ReportPowerStatus(own, 0, cec.PowerOn))},
		{"an unsupported request", cec.NewMessage(0, own, cec.OpMenuRequest, 0x02), ptr(cec.FeatureAbort(own, 0, cec.OpMenuRequest, cec.AbortUnrecognizedOpcode))},
		{"vendor with no vendor to state", cec.GiveDeviceVendorID(0, own), ptr(cec.FeatureAbort(own, 0, cec.OpGiveDeviceVendorID, cec.AbortUnrecognizedOpcode))},
		{"a request from the unregistered address", cec.GiveDevicePowerStatus(15, own), nil},
		{"an unsupported request from the unregistered address", cec.NewMessage(15, own, cec.OpMenuRequest, 0x02), nil},
		{"an answer that arrived late", cec.ReportPowerStatus(0, own, cec.PowerOn), nil},
		{"a remote key", cec.NewMessage(0, own, cec.OpUserControlPressed, 0x00), nil},
		{"a feature abort", cec.FeatureAbort(0, own, cec.OpGiveOSDName, 0), nil},
		{"a broadcast", cec.NewMessage(0, 15, cec.OpMenuRequest), nil},
		{"a poll", cec.Poll(0, own), nil},
		{"another device's message", cec.GiveDevicePowerStatus(0, 5), nil},
		{"its own message", cec.GiveDevicePowerStatus(own, own), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, answers := cec.Answer(c.message, own, cec.PowerOn)
			switch {
			case c.want == nil && answers:
				t.Errorf("answered %v, want no answer", got)
			case c.want != nil && (!answers || got.String() != c.want.String()):
				t.Errorf("answered %v (%v), want %v", got, answers, *c.want)
			}
		})
	}
}

func ptr(message cec.Message) *cec.Message {
	return &message
}
