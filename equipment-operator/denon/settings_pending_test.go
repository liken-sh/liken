// Pending: which declared fields the operator sends, from what the
// receiver reports and what the operator applied before.

package denon

import (
	"encoding/json"
	"testing"
)

func TestPendingKeepsOnlyTheFieldsToSend(t *testing.T) {
	cases := []struct {
		name     string
		want     Settings
		observed Settings
		previous Settings
		known    bool
		pending  Settings
	}{
		{"a reported field at the declared value",
			Settings{Audio: AudioSettings{DRC: strPtr("off")}},
			Settings{Audio: AudioSettings{DRC: strPtr("off")}},
			Settings{}, false,
			Settings{}},
		{"a reported field at another value",
			Settings{Audio: AudioSettings{DRC: strPtr("low"), LFE: intPtr(0)}},
			Settings{Audio: AudioSettings{DRC: strPtr("off"), LFE: intPtr(0)}},
			Settings{}, false,
			Settings{Audio: AudioSettings{DRC: strPtr("low")}}},
		{"a reported HDMI switch at another value",
			Settings{HDMI: HDMISettings{Control: boolPtr(true), ARC: boolPtr(false)}},
			Settings{HDMI: HDMISettings{Control: boolPtr(false), ARC: boolPtr(false)}},
			Settings{}, true,
			Settings{HDMI: HDMISettings{Control: boolPtr(true)}}},
		{"an unreported field after a restart",
			Settings{System: SystemSettings{Eco: strPtr("on")}},
			Settings{},
			Settings{}, false,
			Settings{}},
		{"an unreported field the spec did not change",
			Settings{System: SystemSettings{Eco: strPtr("on")}},
			Settings{},
			Settings{System: SystemSettings{Eco: strPtr("on")}}, true,
			Settings{}},
		{"an unreported field the spec changed",
			Settings{System: SystemSettings{Eco: strPtr("on")}},
			Settings{},
			Settings{System: SystemSettings{Eco: strPtr("off")}}, true,
			Settings{System: SystemSettings{Eco: strPtr("on")}}},
		{"an unreported field the spec added",
			Settings{Tone: ToneSettings{Bass: intPtr(3)}},
			Settings{},
			Settings{}, true,
			Settings{Tone: ToneSettings{Bass: intPtr(3)}}},
		{"a reported channel at the declared trim",
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}},
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}},
			Settings{}, false,
			Settings{}},
		{"a reported channel at another trim",
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5, "FR": 0}},
			Settings{ChannelVolumes: map[string]float64{"FL": 0, "FR": 0}},
			Settings{}, false,
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}}},
		{"an unreported channel after a restart",
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}},
			Settings{},
			Settings{}, false,
			Settings{}},
		{"an unreported channel the spec changed",
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}},
			Settings{},
			Settings{ChannelVolumes: map[string]float64{"FL": 1}}, true,
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}}},
		{"an unreported channel the spec did not change",
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}},
			Settings{},
			Settings{ChannelVolumes: map[string]float64{"FL": 0.5}}, true,
			Settings{}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := one.want.Pending(one.observed, one.previous, one.known)
			mustMatch(t, declaredJSON(t, got), declaredJSON(t, one.pending))
		})
	}
}

// A declared value no command can carry is pending when it would be
// sent, so ApplySettings reports its error instead of the operator
// taking it for settled.
func TestPendingKeepsAValueNoCommandCanCarry(t *testing.T) {
	want := Settings{System: SystemSettings{Eco: strPtr("turbo")}}

	got := want.Pending(Settings{System: SystemSettings{Eco: strPtr("on")}}, Settings{}, false)

	mustMatch(t, *got.System.Eco, "turbo")
}

// declaredJSON writes settings the way the status and the log carry
// them, so two values compare by what they hold and not by pointer.
func declaredJSON(t *testing.T, s Settings) string {
	t.Helper()
	raw, err := json.Marshal(s)
	mustSucceed(t, err)
	return string(raw)
}
