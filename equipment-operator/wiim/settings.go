// The WiiM's slow-moving settings: the strongly typed family the
// operator declares in the Receiver spec and the commands that move
// them. The settings stay in WiiM's own vocabulary, so nothing here is
// shared with another protocol.

package wiim

import (
	"context"
	"fmt"
	"strconv"
)

// Settings is the device's slow-moving settings, one family per block.
// Every optional scalar is a pointer: nil means the key is not
// declared, and a present zero means the key is set to zero. That is
// what lets ApplySettings apply only the declared fields.
type Settings struct {
	Audio  AudioSettings  `json:"audio"`
	Device DeviceSettings `json:"device"`
}

// AudioSettings is the output block's level controls.
type AudioSettings struct {
	Balance *float64 `json:"balance,omitempty"`
}

// DeviceSettings is the device's own switches: the human label, the
// status light, and the touch buttons.
type DeviceSettings struct {
	Name    *string `json:"name,omitempty"`
	LED     *bool   `json:"led,omitempty"`
	Buttons *bool   `json:"buttons,omitempty"`
}

// The small pointer builders the settings share. A pointer field is nil
// when the device has not reported the value, which is how the status
// tells a key the device never declared from one set to zero.
func strPtr(s string) *string     { return &s }
func boolPtr(b bool) *bool        { return &b }
func floatPtr(f float64) *float64 { return &f }

// sameString is true when the two values hold the same word, or either
// is nil. A nil on either side is a key the receiver has not reported,
// so it is skipped rather than judged.
func sameString(a, b *string) bool { return a == nil || b == nil || *a == *b }

// sameBool is true when the two values hold the same switch, or either
// is nil, the way sameString treats an unreported key.
func sameBool(a, b *bool) bool { return a == nil || b == nil || *a == *b }

// sameNumber is true when the two values hold the same number, or
// either is nil, the way sameString treats an unreported key. The
// comparison is exact, because the device echoes the value it was set
// to.
func sameNumber(a, b *float64) bool { return a == nil || b == nil || *a == *b }

// ConfirmedBy answers whether the receiver has reported every field
// the settings declare at the declared value. A declared field the
// receiver has not reported is skipped: there is nothing to confirm it
// against, and blocking on it would retry forever. A field reported at
// another value is not confirmed, so the operator sends it again.
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

// setBalanceCommand builds the wire command for a declared balance. The
// value runs from -1.0, full left, to 1.0, full right. A value outside
// that range is an error that names it.
func setBalanceCommand(value float64) (string, error) {
	if value < -1 || value > 1 {
		return "", fmt.Errorf("balance %.3f is outside -1.0 to 1.0", value)
	}
	return "setChannelBalance:" + strconv.FormatFloat(value, 'f', -1, 64), nil
}

// nameCommand builds the wire command for a declared device name. An
// empty name is an error, because an empty label names nothing.
func nameCommand(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("a device name cannot be empty")
	}
	return "setDeviceName:" + value, nil
}

// switchCommand builds the wire command for a declared switch, prefix
// followed by 1 for on or 0 for off.
func switchCommand(prefix string, value bool) string {
	digit := "0"
	if value {
		digit = "1"
	}
	return prefix + digit
}

// Settings returns every setting the receiver has reported, read from
// the stored status. Each pointer is copied, so the returned value
// never aliases the live state a poll keeps folding into. The values
// are always reported after a successful poll.
func (c *Client) Settings() Settings {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return Settings{
		Audio: AudioSettings{
			Balance: floatPtr(c.state.Audio.Balance),
		},
		Device: DeviceSettings{
			Name:    strPtr(c.state.Device.Name),
			LED:     boolPtr(c.state.Controls.LED),
			Buttons: boolPtr(c.state.Controls.Buttons),
		},
	}
}

// ApplySettings sends the wire command for every declared field, in a
// fixed order: audio before device, and name before led before buttons.
// The controller calls it only when the spec changed, so it does not
// re-assert by itself, and an undeclared field sends nothing. A
// command the receiver refuses stops the apply at that field, and the
// first error is the one returned.
func (c *Client) ApplySettings(want Settings) error {
	if want.Audio.Balance != nil {
		command, err := setBalanceCommand(*want.Audio.Balance)
		if err != nil {
			return err
		}
		if err := c.send(context.Background(), command); err != nil {
			return err
		}
	}
	if want.Device.Name != nil {
		command, err := nameCommand(*want.Device.Name)
		if err != nil {
			return err
		}
		if err := c.send(context.Background(), command); err != nil {
			return err
		}
	}
	if want.Device.LED != nil {
		if err := c.send(context.Background(), switchCommand("LED_SWITCH_SET:", *want.Device.LED)); err != nil {
			return err
		}
	}
	if want.Device.Buttons != nil {
		if err := c.send(context.Background(), switchCommand("Button_Enable_SET:", *want.Device.Buttons)); err != nil {
			return err
		}
	}
	return nil
}
