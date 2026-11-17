// The settings: mapping them from the polled status, applying them to
// the wire, and confirming them against what the receiver reported.

package wiim

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
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

// A read that the model answers with no value leaves the snapshot's zero
// value in place, and the zero value is not the device's setting. So
// Settings reports no value for that field, in each of the device's words
// for a read it cannot serve.
func TestSettingsReportsNoValueForAReadTheModelDoesNotServe(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{"a command the model does not carry", "unknown command"},
		{"a read the model cannot serve", "Failed"},
		{"an empty answer", ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp := startFakeAmp(t)
			amp.answer(ledCommand, one.answer)
			amp.answer(balanceCommand, one.answer)
			client := amp.client(nil)
			client.poll(context.Background())

			settings := client.Settings()
			mustMatch(t, settings.Device.LED, (*bool)(nil))
			mustMatch(t, settings.Audio.Balance, (*float64)(nil))
			mustMatch(t, settings.Device.Buttons, boolPtr(false))
		})
	}
}

// A model's firmware can gain a read. The field is reported again from
// the first answer that holds a value.
func TestSettingsReportsAFieldAgainOnceItsReadAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		amp.answer(ledCommand, "unknown command")
		client := amp.client(nil)
		client.poll(context.Background())
		amp.answer(ledCommand, "1")

		time.Sleep(settingsResync)
		client.poll(context.Background())

		mustMatch(t, client.Settings().Device.LED, boolPtr(true))
	})
}

// One request that fails is not the model's answer, so a field whose read
// answered before stays reported.
func TestSettingsKeepsAFieldWhoseReadFailedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		amp := startFakeAmp(t)
		client := amp.client(nil)
		client.poll(context.Background())
		amp.broken = map[string]bool{ledCommand: true}

		time.Sleep(settingsResync)
		client.poll(context.Background())

		mustMatch(t, client.Settings().Device.LED, boolPtr(true))
	})
}

// controlReads are the reads of the controls family, which a sent light
// or button switch is read back through.
var controlReads = []string{ledCommand, buttonsCommand, cecCommand}

// ApplySettings sends exactly the declared commands, in the fixed audio
// then device order, and sends nothing for an undeclared field. Then it
// reads back the family of each command the amp took.
func TestApplySettingsSendsDeclaredCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		want Settings
		sent []string
	}{
		{"balance", Settings{Audio: AudioSettings{Balance: floatPtr(0.25)}}, []string{"setChannelBalance:0.25", balanceCommand}},
		{"name", Settings{Device: DeviceSettings{Name: strPtr("Office")}}, []string{"setDeviceName:Office", statusCommand}},
		{"led on", Settings{Device: DeviceSettings{LED: boolPtr(true)}}, append([]string{"LED_SWITCH_SET:1"}, controlReads...)},
		{"led off", Settings{Device: DeviceSettings{LED: boolPtr(false)}}, append([]string{"LED_SWITCH_SET:0"}, controlReads...)},
		{"buttons on", Settings{Device: DeviceSettings{Buttons: boolPtr(true)}}, append([]string{"Button_Enable_SET:1"}, controlReads...)},
		{"buttons off", Settings{Device: DeviceSettings{Buttons: boolPtr(false)}}, append([]string{"Button_Enable_SET:0"}, controlReads...)},
		{"combined", Settings{
			Audio:  AudioSettings{Balance: floatPtr(-0.5)},
			Device: DeviceSettings{Name: strPtr("Studio"), LED: boolPtr(true), Buttons: boolPtr(false)},
		}, append([]string{"setChannelBalance:-0.5", "setDeviceName:Studio", "LED_SWITCH_SET:1", "Button_Enable_SET:0", balanceCommand, statusCommand}, controlReads...)},
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
