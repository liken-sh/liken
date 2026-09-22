// The bus commands. Set moves one declared setting and Do runs one
// action. Each maps a stable dotted id to the wire command it sends,
// in WiiM's own vocabulary, so a controller that subscribes to a
// settings topic and a commands topic translates one message into one
// device command. The slow-moving settings live in settings.go; these
// two are the one-shot keyed writes from the bus.

package wiim

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/liken-sh/equipment-operator/equipment"
)

// settingSpec is one row of the settings id table: a stable bus id and
// the wire command a value of the right kind builds. An id not in the
// table is an error that names it, and a value of the wrong kind is an
// error that names the id and the kind it wanted.
type settingSpec struct {
	id    string
	kind  string
	build func(equipment.SettingValue) (string, error)
}

// settingsById is the assembled settings id table. Each build reuses
// the command builder from settings.go, so a setting speaks the same
// wire command whether the spec declares it or the bus writes it.
var settingsById = map[string]settingSpec{
	"audio.balance": {
		id:   "audio.balance",
		kind: "a number",
		build: func(v equipment.SettingValue) (string, error) {
			value, ok := v.Number()
			if !ok {
				return "", settingTypeError("audio.balance", "a number")
			}
			return setBalanceCommand(value)
		},
	},
	"device.name": {
		id:   "device.name",
		kind: "a string",
		build: func(v equipment.SettingValue) (string, error) {
			value, ok := v.String()
			if !ok {
				return "", settingTypeError("device.name", "a string")
			}
			return nameCommand(value)
		},
	},
	"device.led": {
		id:   "device.led",
		kind: "a boolean",
		build: func(v equipment.SettingValue) (string, error) {
			value, ok := v.Bool()
			if !ok {
				return "", settingTypeError("device.led", "a boolean")
			}
			return switchCommand("LED_SWITCH_SET:", value), nil
		},
	},
	"device.buttons": {
		id:   "device.buttons",
		kind: "a boolean",
		build: func(v equipment.SettingValue) (string, error) {
			value, ok := v.Bool()
			if !ok {
				return "", settingTypeError("device.buttons", "a boolean")
			}
			return switchCommand("Button_Enable_SET:", value), nil
		},
	},
}

// Set builds the wire command for one keyed write from the bus and
// sends it. An unknown id is an error that names it, and a value of
// the wrong kind is an error that names the id and the kind it wanted.
func (c *Client) Set(id string, value equipment.SettingValue) error {
	spec, held := settingsById[id]
	if !held {
		return fmt.Errorf("unknown setting %q", id)
	}
	command, err := spec.build(value)
	if err != nil {
		return err
	}
	return c.send(context.Background(), command)
}

// settingTypeError is the error a setting that received the wrong kind
// of value carries.
func settingTypeError(id, kind string) error {
	return fmt.Errorf("setting %s needs %s", id, kind)
}

// commandSpec is one row of the action id table: a stable bus id and
// the wire command its arguments build. An id not in the table is an
// error that names it.
type commandSpec struct {
	id    string
	build func(map[string]equipment.SettingValue) (string, error)
}

// commandsById is the assembled action id table.
var commandsById = map[string]commandSpec{
	"preset.recall": {
		id: "preset.recall",
		build: func(args map[string]equipment.SettingValue) (string, error) {
			number, err := commandNumber(args, "preset.recall", "number", 1, 12)
			if err != nil {
				return "", err
			}
			return "MCUKeyShortClick:" + strconv.Itoa(number), nil
		},
	},
	"bluetooth.scan": {
		id: "bluetooth.scan",
		build: func(args map[string]equipment.SettingValue) (string, error) {
			seconds, err := commandNumber(args, "bluetooth.scan", "seconds", 1, 60)
			if err != nil {
				return "", err
			}
			return "startbtdiscovery:" + strconv.Itoa(seconds), nil
		},
	},
	"bluetooth.connect": {
		id: "bluetooth.connect",
		build: func(args map[string]equipment.SettingValue) (string, error) {
			address, err := commandString(args, "bluetooth.connect", "address")
			if err != nil {
				return "", err
			}
			return "connectbta2dpsynk:" + address, nil
		},
	},
	"bluetooth.disconnect": {
		id: "bluetooth.disconnect",
		build: func(args map[string]equipment.SettingValue) (string, error) {
			address, err := commandString(args, "bluetooth.disconnect", "address")
			if err != nil {
				return "", err
			}
			return "disconnectbta2dpsynk:" + address, nil
		},
	},
	"reboot": {
		id: "reboot",
		build: func(args map[string]equipment.SettingValue) (string, error) {
			return "reboot", nil
		},
	},
}

// commandNumber reads one whole-number argument, in the given range.
// A missing argument, a value that is not a whole number, or a value
// out of range is an error naming the command and the argument.
func commandNumber(args map[string]equipment.SettingValue, command, arg string, min, max int) (int, error) {
	value, held := args[arg]
	if !held {
		return 0, fmt.Errorf("%s needs argument %q", command, arg)
	}
	number, ok := value.Number()
	if !ok || number != math.Trunc(number) {
		return 0, fmt.Errorf("%s needs argument %q as a whole number", command, arg)
	}
	whole := int(number)
	if whole < min || whole > max {
		return 0, fmt.Errorf("%s argument %q %d is outside %d to %d", command, arg, whole, min, max)
	}
	return whole, nil
}

// commandString reads one string argument. A missing argument, a
// value that is not a string, or an empty value is an error naming the
// command and the argument.
func commandString(args map[string]equipment.SettingValue, command, arg string) (string, error) {
	value, held := args[arg]
	if !held {
		return "", fmt.Errorf("%s needs argument %q", command, arg)
	}
	text, ok := value.String()
	if !ok {
		return "", fmt.Errorf("%s needs argument %q as a string", command, arg)
	}
	if text == "" {
		return "", fmt.Errorf("%s argument %q cannot be empty", command, arg)
	}
	return text, nil
}

// Do builds and sends the wire command for one one-shot action from
// the bus. An unknown id is an error that names it.
func (c *Client) Do(id string, args map[string]equipment.SettingValue) error {
	spec, held := commandsById[id]
	if !held {
		return fmt.Errorf("unknown command %q", id)
	}
	command, err := spec.build(args)
	if err != nil {
		return err
	}
	return c.send(context.Background(), command)
}
