// Pending: which declared fields the operator sends to the device.

package wiim

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
		{"every field at the declared value",
			Settings{Audio: AudioSettings{Balance: floatPtr(0)}, Device: DeviceSettings{Name: strPtr("Lab"), LED: boolPtr(true), Buttons: boolPtr(false)}},
			Settings{Audio: AudioSettings{Balance: floatPtr(0)}, Device: DeviceSettings{Name: strPtr("Lab"), LED: boolPtr(true), Buttons: boolPtr(false)}},
			Settings{}, false,
			Settings{}},
		{"one field at another value",
			Settings{Device: DeviceSettings{Name: strPtr("Lab"), LED: boolPtr(true)}},
			Settings{Device: DeviceSettings{Name: strPtr("Lab"), LED: boolPtr(false)}},
			Settings{}, false,
			Settings{Device: DeviceSettings{LED: boolPtr(true)}}},
		{"a balance at another value",
			Settings{Audio: AudioSettings{Balance: floatPtr(0.5)}},
			Settings{Audio: AudioSettings{Balance: floatPtr(0)}},
			Settings{}, true,
			Settings{Audio: AudioSettings{Balance: floatPtr(0.5)}}},
		{"an unreported field after a restart",
			Settings{Device: DeviceSettings{Buttons: boolPtr(true)}},
			Settings{},
			Settings{}, false,
			Settings{}},
		{"an unreported field the spec did not change",
			Settings{Device: DeviceSettings{Buttons: boolPtr(true)}},
			Settings{},
			Settings{Device: DeviceSettings{Buttons: boolPtr(true)}}, true,
			Settings{}},
		{"an unreported field the spec changed",
			Settings{Device: DeviceSettings{Name: strPtr("Lab")}},
			Settings{},
			Settings{Device: DeviceSettings{Name: strPtr("Office")}}, true,
			Settings{Device: DeviceSettings{Name: strPtr("Lab")}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got := one.want.Pending(one.observed, one.previous, one.known)
			mustMatch(t, settingsJSON(t, got), settingsJSON(t, one.pending))
		})
	}
}

// settingsJSON writes settings the way the log carries them, so two
// values compare by what they hold and not by pointer.
func settingsJSON(t *testing.T, s Settings) string {
	t.Helper()
	raw, err := json.Marshal(s)
	mustSucceed(t, err)
	return string(raw)
}
