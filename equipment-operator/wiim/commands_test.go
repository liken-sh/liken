// The one-shot command translation. Set moves one declared setting and
// Do runs one action, each mapped to the wire command it sends.

package wiim

import (
	"strings"
	"testing"

	"github.com/liken-sh/equipment-operator/equipment"
)

// Each declared setting reaches its own wire command.
func TestSetReachesItsWireCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name  string
		id    string
		value equipment.SettingValue
		want  string
	}{
		{"balance full left", "audio.balance", equipment.NumberSettingValue(-1), "setChannelBalance:-1"},
		{"balance center", "audio.balance", equipment.NumberSettingValue(0), "setChannelBalance:0"},
		{"balance full right", "audio.balance", equipment.NumberSettingValue(1), "setChannelBalance:1"},
		{"name", "device.name", equipment.StringSettingValue("Studio"), "setDeviceName:Studio"},
		{"led on", "device.led", equipment.BoolSettingValue(true), "LED_SWITCH_SET:1"},
		{"led off", "device.led", equipment.BoolSettingValue(false), "LED_SWITCH_SET:0"},
		{"buttons on", "device.buttons", equipment.BoolSettingValue(true), "Button_Enable_SET:1"},
		{"buttons off", "device.buttons", equipment.BoolSettingValue(false), "Button_Enable_SET:0"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp.mutex.Lock()
			amp.commands = nil
			amp.mutex.Unlock()
			mustSucceed(t, client.Set(one.id, one.value))
			mustMatch(t, amp.sent(), []string{one.want})
		})
	}
}

// Set rejects an id it does not know, a value of the wrong kind, and
// a value no command can carry.
func TestSetRejectsBadSettings(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name  string
		id    string
		value equipment.SettingValue
		want  string
	}{
		{"unknown id", "no.such", equipment.NumberSettingValue(0), "unknown setting"},
		{"balance out of range", "audio.balance", equipment.NumberSettingValue(2), "outside"},
		{"balance wrong kind", "audio.balance", equipment.BoolSettingValue(true), "needs a number"},
		{"name empty", "device.name", equipment.StringSettingValue(""), "cannot be empty"},
		{"name wrong kind", "device.name", equipment.NumberSettingValue(1), "needs a string"},
		{"led wrong kind", "device.led", equipment.NumberSettingValue(1), "needs a boolean"},
		{"buttons wrong kind", "device.buttons", equipment.StringSettingValue("x"), "needs a boolean"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := client.Set(one.id, one.value)
			if err == nil || !strings.Contains(err.Error(), one.want) {
				t.Fatalf("got %v, want error containing %q", err, one.want)
			}
		})
	}
}

// Each action reaches its own wire command.
func TestDoReachesItsWireCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		id   string
		args map[string]equipment.SettingValue
		want string
	}{
		{"recall preset one", "preset.recall", map[string]equipment.SettingValue{"number": equipment.NumberSettingValue(1)}, "MCUKeyShortClick:1"},
		{"recall preset", "preset.recall", map[string]equipment.SettingValue{"number": equipment.NumberSettingValue(3)}, "MCUKeyShortClick:3"},
		{"recall preset twelve", "preset.recall", map[string]equipment.SettingValue{"number": equipment.NumberSettingValue(12)}, "MCUKeyShortClick:12"},
		{"scan", "bluetooth.scan", map[string]equipment.SettingValue{"seconds": equipment.NumberSettingValue(10)}, "startbtdiscovery:10"},
		{"connect", "bluetooth.connect", map[string]equipment.SettingValue{"address": equipment.StringSettingValue("00:11:22:33:44:55")}, "connectbta2dpsynk:00:11:22:33:44:55"},
		{"disconnect", "bluetooth.disconnect", map[string]equipment.SettingValue{"address": equipment.StringSettingValue("00:11:22:33:44:55")}, "disconnectbta2dpsynk:00:11:22:33:44:55"},
		{"reboot", "reboot", nil, "reboot"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp.mutex.Lock()
			amp.commands = nil
			amp.mutex.Unlock()
			mustSucceed(t, client.Do(one.id, one.args))
			mustMatch(t, amp.sent(), []string{one.want})
		})
	}
}

// Do rejects an id it does not know, a missing or wrong-kind argument,
// an out-of-range number, and an empty address.
func TestDoRejectsBadCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		id   string
		args map[string]equipment.SettingValue
		want string
	}{
		{"unknown id", "no.such", nil, "unknown command"},
		{"missing number", "preset.recall", nil, `argument "number"`},
		{"recall wrong kind", "preset.recall", map[string]equipment.SettingValue{"number": equipment.StringSettingValue("3")}, "whole number"},
		{"recall out of range", "preset.recall", map[string]equipment.SettingValue{"number": equipment.NumberSettingValue(13)}, "outside"},
		{"recall under one", "preset.recall", map[string]equipment.SettingValue{"number": equipment.NumberSettingValue(0)}, "outside"},
		{"scan out of range", "bluetooth.scan", map[string]equipment.SettingValue{"seconds": equipment.NumberSettingValue(61)}, "outside"},
		{"missing address", "bluetooth.connect", nil, `argument "address"`},
		{"empty address", "bluetooth.connect", map[string]equipment.SettingValue{"address": equipment.StringSettingValue("")}, "cannot be empty"},
		{"address wrong kind", "bluetooth.disconnect", map[string]equipment.SettingValue{"address": equipment.NumberSettingValue(1)}, "as a string"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := client.Do(one.id, one.args)
			if err == nil || !strings.Contains(err.Error(), one.want) {
				t.Fatalf("got %v, want error containing %q", err, one.want)
			}
		})
	}
}

// A command the device refuses carries the device's own words.
func TestACommandFailureCarriesTheDeviceWords(t *testing.T) {
	amp := startFakeAmp(t)
	amp.mutex.Lock()
	amp.setter = "unknown command"
	amp.mutex.Unlock()
	client := amp.client(nil)

	err := client.Do("reboot", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("a refused command did not carry the device's words: %v", err)
	}
}
