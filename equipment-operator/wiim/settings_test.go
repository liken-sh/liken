// Settings: reading them from the polled status, sending them to the
// device, and comparing them with what the device reported.

package wiim

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// Settings returns a pointer to each value the poll read.
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

// When a model answers a read with no value, the stored status keeps a
// zero value that is not the device's setting. So Settings returns nil
// for that field, for each way a device says it has no value.
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

// After a firmware update adds a read, Settings returns the field again
// from the first answer that has a value.
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

// A request that fails once says nothing about the model, so a field
// that was read before is still returned.
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

// controlReads are the commands that read the controls family, which
// ApplySettings runs after it sends an LED or button command.
var controlReads = []string{ledCommand, buttonsCommand, cecCommand}

// ApplySettings sends one command per field that is set, in a fixed
// order, and nothing for a nil field. Then it reads back the family of
// each command the amp accepted.
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

// ApplySettings returns an error and sends nothing for a balance outside
// the range or an empty name.
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
				t.Fatalf("ApplySettings for %s returned no error, want one", one.name)
			}
			mustMatch(t, amp.sent(), []string(nil))
		})
	}
}

// ConfirmedBy is true when each field that is set matches the observed
// value, false when one differs, and skips a field that is nil.
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

	// Only the balance is set, so the other observed values do not
	// matter.
	mustMatch(t, Settings{Audio: AudioSettings{Balance: floatPtr(0.5)}}.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Anything"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), true)
	// With no field set, any observed settings confirm.
	mustMatch(t, Settings{}.ConfirmedBy(Settings{
		Audio:  AudioSettings{Balance: floatPtr(0.5)},
		Device: DeviceSettings{Name: strPtr("Anything"), LED: boolPtr(true), Buttons: boolPtr(false)},
	}), true)
}

// ApplySettings returns the error of the first command the receiver
// refuses.
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
				t.Fatalf("ApplySettings for %s returned no error, want one", one.name)
			}
		})
	}
}
