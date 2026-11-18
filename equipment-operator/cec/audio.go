package cec

// System Audio Control (HDMI-CEC 1.3a, CEC 13.15). While System Audio
// Mode is on, the TV mutes its own speakers and sends the volume keys
// of its remote to the audio system at logical address 5. A device
// turns the mode on with System Audio Mode Request, which carries the
// physical address of the source to play. The audio system leaves
// standby if it must, switches to the input for that address, and
// replies with Set System Audio Mode [On]. Just before it enters
// standby, it broadcasts Set System Audio Mode [Off].
//
// PressVolumeKey is here because CEC Table 25 requires a device that
// sends System Audio Mode Request to be able to send User Control
// Pressed for Volume Up, Volume Down, and Mute, and User Control
// Released. Nothing in the operator calls it, because the Receiver's
// network driver sets the volume.

import "fmt"

// SystemAudioMode is the System Audio Mode that the audio system last
// reported. Off is 0 on the wire, but the zero value here is unknown, so
// a Directory can tell a reported Off from a mode that was never
// reported.
type SystemAudioMode byte

const (
	SystemAudioUnknown SystemAudioMode = iota
	SystemAudioOff
	SystemAudioOn
)

// audioModeOf converts the [System Audio Status] operand of Set System
// Audio Mode and System Audio Mode Status. 0 is Off and 1 is On (CEC
// Table 26), and any other value is unknown.
func audioModeOf(operand byte) SystemAudioMode {
	switch operand {
	case 0:
		return SystemAudioOff
	case 1:
		return SystemAudioOn
	}
	return SystemAudioUnknown
}

// operand returns 0, the wire value of Off, for an unknown mode.
func (m SystemAudioMode) operand() byte {
	if m == SystemAudioOn {
		return 1
	}
	return 0
}

// String returns the value of a device's status.systemAudioMode in a
// CECBus, which is empty for an unknown mode.
func (m SystemAudioMode) String() string {
	switch m {
	case SystemAudioOn:
		return "On"
	case SystemAudioOff:
		return "Off"
	}
	return ""
}

// SystemAudioModeRequest builds a request that turns System Audio Mode
// on for the source at address. The address is required, because a
// System Audio Mode Request with no operand turns the mode off.
func SystemAudioModeRequest(from LogicalAddress, address PhysicalAddress) Message {
	return NewMessage(from, AddressAudioSystem, OpSystemAudioModeRequest, byte(address>>8), byte(address))
}

// GiveSystemAudioModeStatus builds a request for the audio system's
// current mode. The audio system replies only while it is out of
// standby.
func GiveSystemAudioModeStatus(from LogicalAddress) Message {
	return NewMessage(from, AddressAudioSystem, OpGiveSystemAudioModeState)
}

// SetSystemAudioMode builds the message in which the audio system
// reports its mode. When a device other than the TV starts the mode,
// the audio system sends the first [On] directly to the TV, and
// broadcasts the later ones (CEC 13.15.2).
func SetSystemAudioMode(from, to LogicalAddress, mode SystemAudioMode) Message {
	return NewMessage(from, to, OpSetSystemAudioMode, mode.operand())
}

func SystemAudioModeStatus(from, to LogicalAddress, mode SystemAudioMode) Message {
	return NewMessage(from, to, OpSystemAudioModeStatus, mode.operand())
}

// ReadSystemAudioMode asks the audio system for its mode once and
// records the reply in the directory. It returns SystemAudioUnknown
// when the audio system does not reply or replies with a Feature Abort,
// and the Result tells which. The error is non-nil only when the kernel
// refuses the transmit.
func ReadSystemAudioMode(device *Device, directory *Directory, own LogicalAddress) (SystemAudioMode, Result, error) {
	result, err := device.Transmit(GiveSystemAudioModeStatus(own), OpSystemAudioModeStatus, replyTimeout)
	if err != nil || result.Reply == nil {
		return SystemAudioUnknown, result, err
	}
	directory.Observe(*result.Reply)
	operands := result.Reply.Operands()
	if len(operands) == 0 {
		return SystemAudioUnknown, result, nil
	}
	return audioModeOf(operands[0]), result, nil
}

// VolumeKey is a UI command of User Control Pressed that changes the
// audio system's volume (HDMI-CEC 1.3a, CEC Table 27).
type VolumeKey byte

const (
	KeyVolumeUp   VolumeKey = 0x41
	KeyVolumeDown VolumeKey = 0x42
	KeyMute       VolumeKey = 0x43
)

// PressVolumeKey sends User Control Pressed to the audio system and
// then User Control Released at once, so the volume changes by one
// step. It sends no release when the audio system did not acknowledge
// the press, and returns the Result of the press. The error is non-nil
// when key is not a volume key or the kernel refuses a transmit.
func PressVolumeKey(device *Device, own LogicalAddress, key VolumeKey) (Result, error) {
	switch key {
	case KeyVolumeUp, KeyVolumeDown, KeyMute:
	default:
		return Result{}, fmt.Errorf("key 0x%02x is not a volume key, want 0x41, 0x42, or 0x43", byte(key))
	}
	pressed, err := device.Transmit(NewMessage(own, AddressAudioSystem, OpUserControlPressed, byte(key)), 0, 0)
	if err != nil || !pressed.Acked {
		return pressed, err
	}
	_, err = device.Transmit(NewMessage(own, AddressAudioSystem, OpUserControlReleased), 0, 0)
	return pressed, err
}
