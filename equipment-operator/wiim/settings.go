// The WiiM settings that change rarely: the typed fields that the
// Receiver spec declares, and the commands that set them on the device.

package wiim

import (
	"context"
	"fmt"
	"strconv"
)

// Settings holds the device settings that change rarely. Each field is
// a pointer, so nil means not set and is different from a zero value.
type Settings struct {
	Audio  AudioSettings  `json:"audio"`
	Device DeviceSettings `json:"device"`
}

type AudioSettings struct {
	Balance *float64 `json:"balance,omitempty"`
}

type DeviceSettings struct {
	Name    *string `json:"name,omitempty"`
	LED     *bool   `json:"led,omitempty"`
	Buttons *bool   `json:"buttons,omitempty"`
}

// strPtr, boolPtr, and floatPtr return a pointer to a copy of a value,
// for the pointer fields of Settings.
func strPtr(s string) *string     { return &s }
func boolPtr(b bool) *bool        { return &b }
func floatPtr(f float64) *float64 { return &f }

// sameString is true when the two values are equal or either is nil. A
// nil value is a field with nothing to compare, so it counts as equal.
func sameString(a, b *string) bool { return a == nil || b == nil || *a == *b }

// sameBool is sameString for bool values.
func sameBool(a, b *bool) bool { return a == nil || b == nil || *a == *b }

// sameNumber is sameString for float64 values. The comparison is
// exact, because the device returns the value that was sent to it.
func sameNumber(a, b *float64) bool { return a == nil || b == nil || *a == *b }

// ConfirmedBy reports whether every field set in w has the same value in
// observed, the settings the receiver reported. It skips a field that
// observed does not set, because the receiver cannot confirm that field
// and a wait for it would never end.
func (w Settings) ConfirmedBy(observed Settings) bool {
	if !sameNumber(w.Audio.Balance, observed.Audio.Balance) {
		return false
	}
	if !sameString(w.Device.Name, observed.Device.Name) {
		return false
	}
	if !sameBool(w.Device.LED, observed.Device.LED) {
		return false
	}
	if !sameBool(w.Device.Buttons, observed.Device.Buttons) {
		return false
	}
	return true
}

// Pending returns the fields of w that must be sent. A field that the
// device reports is sent while the device's value differs, so a restart
// sends nothing to a device that already has the declared values, and a
// change made at the device is reverted. A field that the device does
// not report is sent when it differs from previous, the settings the
// operator last applied, and only when known is true. The controller
// sets known to false when it has applied nothing since it started and
// status.settledSettings records that it already sent this block.
func (w Settings) Pending(observed, previous Settings, known bool) Settings {
	var pending Settings
	if isPending(w.Audio.Balance, observed.Audio.Balance, previous.Audio.Balance, known) {
		pending.Audio.Balance = w.Audio.Balance
	}
	if isPending(w.Device.Name, observed.Device.Name, previous.Device.Name, known) {
		pending.Device.Name = w.Device.Name
	}
	if isPending(w.Device.LED, observed.Device.LED, previous.Device.LED, known) {
		pending.Device.LED = w.Device.LED
	}
	if isPending(w.Device.Buttons, observed.Device.Buttons, previous.Device.Buttons, known) {
		pending.Device.Buttons = w.Device.Buttons
	}
	return pending
}

// isPending applies the rule of Pending to one field.
func isPending[T comparable](want, observed, previous *T, known bool) bool {
	switch {
	case want == nil:
		return false
	case observed != nil:
		return *observed != *want
	}
	return known && (previous == nil || *previous != *want)
}

// setBalanceCommand returns the command that sets the balance, from -1.0
// (full left) to 1.0 (full right). It returns an error for a value
// outside that range.
func setBalanceCommand(value float64) (string, error) {
	if value < -1 || value > 1 {
		return "", fmt.Errorf("the balance is %.3f, and it must be from -1.0 to 1.0", value)
	}
	return "setChannelBalance:" + strconv.FormatFloat(value, 'f', -1, 64), nil
}

// nameCommand returns the command that sets the device name. It returns
// an error for an empty name.
func nameCommand(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("the device name must not be empty")
	}
	return "setDeviceName:" + value, nil
}

// switchCommand returns prefix followed by 1 for on or 0 for off.
func switchCommand(prefix string, value bool) string {
	digit := "0"
	if value {
		digit = "1"
	}
	return prefix + digit
}

// Settings returns the settings from the last poll, as copies.
//
// A field is nil when the device answered its read with no value, as a
// model with no read for the status light does. Without that, the zero
// value in c.state would read as "off", and the controller would send a
// declared "on" on every pass. The name is always set, because every
// poll reads it.
func (c *Client) Settings() Settings {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	settings := Settings{Device: DeviceSettings{Name: strPtr(c.state.Device.Name)}}
	if !c.unread[balanceCommand] {
		settings.Audio.Balance = floatPtr(c.state.Audio.Balance)
	}
	if !c.unread[ledCommand] {
		settings.Device.LED = boolPtr(c.state.Controls.LED)
	}
	if !c.unread[buttonsCommand] {
		settings.Device.Buttons = boolPtr(c.state.Controls.Buttons)
	}
	return settings
}

// ApplySettings sends the command for each field set in want, in the
// order balance, name, LED, buttons. It stops at the first command the
// receiver refuses and returns that error. Then it reads back the family
// of each command the receiver accepted, so the controller's next
// comparison sees the new values.
func (c *Client) ApplySettings(want Settings) error {
	var accepted family
	err := c.sendSettings(want, &accepted)
	c.readBack(context.Background(), accepted)
	return err
}

// sendSettings sends the command for each field set in want, stops at
// the first error, and adds the family of each accepted command to
// accepted.
func (c *Client) sendSettings(want Settings, accepted *family) error {
	if want.Audio.Balance != nil {
		command, err := setBalanceCommand(*want.Audio.Balance)
		if err != nil {
			return err
		}
		if err := c.send(context.Background(), command); err != nil {
			return err
		}
		*accepted |= familyBalance
	}
	if want.Device.Name != nil {
		command, err := nameCommand(*want.Device.Name)
		if err != nil {
			return err
		}
		if err := c.send(context.Background(), command); err != nil {
			return err
		}
		*accepted |= familyName
	}
	if want.Device.LED != nil {
		if err := c.send(context.Background(), switchCommand("LED_SWITCH_SET:", *want.Device.LED)); err != nil {
			return err
		}
		*accepted |= familyControls
	}
	if want.Device.Buttons != nil {
		if err := c.send(context.Background(), switchCommand("Button_Enable_SET:", *want.Device.Buttons)); err != nil {
			return err
		}
		*accepted |= familyControls
	}
	return nil
}
