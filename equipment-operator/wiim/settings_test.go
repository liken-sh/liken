// The settings: mapping them from the polled status, applying them to
// the wire, and confirming them against what the receiver reported.

package wiim

import (
	"context"
	"testing"
)

// Settings reads the slow-moving settings from the polled status, one
// pointer per reported value.
func TestSettingsMapsThePolledStatus(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())

	settings := client.Settings()
	mustMatch(t, settings.Audio.Balance, floatPtr(0.5))
	mustMatch(t, settings.Device.Name, strPtr("Test Amp"))
	mustMatch(t, settings.Device.LED, boolPtr(true))
	mustMatch(t, settings.Device.Buttons, boolPtr(false))
}

// ApplySettings sends exactly the declared commands, in the fixed audio
// then device order, and sends nothing for an undeclared field.
func TestApplySettingsSendsDeclaredCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		want Settings
		sent []string
	}{
		{"balance", Settings{Audio: AudioSettings{Balance: floatPtr(0.25)}}, []string{"setChannelBalance:0.25"}},
		{"name", Settings{Device: DeviceSettings{Name: strPtr("Office")}}, []string{"setDeviceName:Office"}},
		{"led on", Settings{Device: DeviceSettings{LED: boolPtr(true)}}, []string{"LED_SWITCH_SET:1"}},
		{"led off", Settings{Device: DeviceSettings{LED: boolPtr(false)}}, []string{"LED_SWITCH_SET:0"}},
		{"buttons on", Settings{Device: DeviceSettings{Buttons: boolPtr(true)}}, []string{"Button_Enable_SET:1"}},
		{"buttons off", Settings{Device: DeviceSettings{Buttons: boolPtr(false)}}, []string{"Button_Enable_SET:0"}},
		{"combined", Settings{
			Audio:  AudioSettings{Balance: floatPtr(-0.5)},
			Device: DeviceSettings{Name: strPtr("Studio"), LED: boolPtr(true), Buttons: boolPtr(false)},
		}, []string{"setChannelBalance:-0.5", "setDeviceName:Studio", "LED_SWITCH_SET:1", "Button_Enable_SET:0"}},
		{"none declared", Settings{}, nil},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp.mutex.Lock()
			amp.commands = nil
			amp.mutex.Unlock()
			mustSucceed(t, client.ApplySettings(one.want))
			mustMatch(t, amp.sent(), one.sent)
		})
	}
}

// ApplySettings rejects a balance outside the device's range and an
// empty device name, and sends nothing on the refusal.
func TestApplySettingsRejectsWhatTheDeviceCannotDo(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		want Settings
	}{
		{"balance under -1", Settings{Audio: AudioSettings{Balance: floatPtr(-1.5)}}},
		{"balance over 1", Settings{Audio: AudioSettings{Balance: floatPtr(1.5)}}},
		{"empty name", Settings{Device: DeviceSettings{Name: strPtr("")}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp.mutex.Lock()
			amp.commands = nil
			amp.mutex.Unlock()
			if err := client.ApplySettings(one.want); err == nil {
				t.Fatalf("%s did not error", one.name)
			}
			mustMatch(t, amp.sent(), []string(nil))
		})
	}
}

// ConfirmedBy confirms a field the receiver reports at the declared
// value, rejects a field reported at another value, and skips a field
// the settings do not declare.
func TestConfirmedBy(t *testing.T) {
	declared := Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Test Amp"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}
	equal := Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Test Amp"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}

	mustMatch(t, declared.ConfirmedBy(equal), true)

	mustMatch(t, declared.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.25)},
		Device: DeviceSettings{Name: strPtr("Test Amp"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), false)
	mustMatch(t, declared.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Other"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), false)
	mustMatch(t, declared.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Test Amp"), LED: boolPtr(false), Buttons: boolPtr(false)},
	}), false)
	mustMatch(t, declared.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Test Amp"), LED: boolPtr(true), Buttons: boolPtr(true)},
	}), false)

	// A declared balance with nothing else declared confirms against a
	// receiver that reports every field at another value: the fields
	// the settings do not declare are skipped.
	mustMatch(t, Settings{Audio: AudioSettings{Balance: floatPtr(0.5)}}.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Anything"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), true)
	// A settings with nothing declared confirms against any report.
	mustMatch(t, Settings{}.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Anything"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), true)
}

// ApplySettings stops at the first command the receiver refuses, and
// the refusal is the error it returns.
func TestApplySettingsStopsOnAFailedCommand(t *testing.T) {
	amp := startFakeAmp(t)
	amp.mutex.Lock()
	amp.setter = "unknown command"
	amp.mutex.Unlock()
	client := amp.client(nil)

	cases := []struct {
		name string
		want Settings
	}{
		{"balance", Settings{Audio: AudioSettings{Balance: floatPtr(0.5)}}},
		{"name", Settings{Device: DeviceSettings{Name: strPtr("Office")}}},
		{"led", Settings{Device: DeviceSettings{LED: boolPtr(true)}}},
		{"buttons", Settings{Device: DeviceSettings{Buttons: boolPtr(true)}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if err := client.ApplySettings(one.want); err == nil {
				t.Fatalf("%s did not error", one.name)
			}
		})
	}
}
