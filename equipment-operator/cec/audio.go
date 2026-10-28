package cec

// System Audio Control (HDMI-CEC 1.3a, CEC 13.15). While System Audio
// Mode is on, the TV mutes its own speakers and sends the volume keys
// of its remote to the audio system at logical address 5. A device asks
// the audio system for the mode with System Audio Mode Request, which
// carries the physical address of the source to play: the audio system
// comes out of standby if necessary, switches to the input for that
// address, and answers with Set System Audio Mode [On]. The audio
// system broadcasts Set System Audio Mode [Off] just before standby.
//
// This file holds the messages of the feature, the read of the mode,
// and the volume keys. CEC Table 25 says that a device that sends
// System Audio Mode Request shall be able to send User Control Pressed
// for Volume Up, Volume Down, and Mute, with User Control Released, so
// the keys are here although the node workload sets the volume over
// the network and never presses them.

import "fmt"

// SystemAudioMode is what the audio system last stated about System
// Audio Mode. The zero value is unknown, because the wire operand of
// Off is zero and a directory must tell an Off it heard from a mode it
// never heard.
type SystemAudioMode byte

const (
	SystemAudioUnknown SystemAudioMode = iota
	SystemAudioOff
	SystemAudioOn
)

// audioModeOf reads the [System Audio Status] operand of Set System
// Audio Mode and System Audio Mode Status: 0 is Off and 1 is On (CEC
// Table 29). Any other value is unknown.
func audioModeOf(operand byte) SystemAudioMode {
	switch operand {
	case 0:
		return SystemAudioOff
	case 1:
		return SystemAudioOn
	}
	return SystemAudioUnknown
}

// operand is the wire value of a known mode.
func (m SystemAudioMode) operand() byte {
	if m == SystemAudioOn {
		return 1
	}
	return 0
}

// String names the mode the way the status reports it, and an unknown
// mode as empty.
func (m SystemAudioMode) String() string {
	switch m {
	case SystemAudioOn:
		return "On"
	case SystemAudioOff:
		return "Off"
	}
	return ""
}

// SystemAudioModeRequest asks the audio system for System Audio Mode
// with the sound of the source at a physical address. The node
// workload never sends the request without an operand, because a
// request with no operand asks the audio system to turn the mode off.
func SystemAudioModeRequest(from LogicalAddress, address PhysicalAddress) Message {
	return NewMessage(from, AddressAudioSystem, OpSystemAudioModeRequest, byte(address>>8), byte(address))
}

// GiveSystemAudioModeStatus asks the audio system for the mode. The
// audio system answers with System Audio Mode Status while it is out of
// standby.
func GiveSystemAudioModeStatus(from LogicalAddress) Message {
	return NewMessage(from, AddressAudioSystem, OpGiveSystemAudioModeState)
}

// SetSystemAudioMode is the audio system's statement of the mode, which
// it broadcasts, or directs to the TV the first time after a request.
func SetSystemAudioMode(from, to LogicalAddress, mode SystemAudioMode) Message {
	return NewMessage(from, to, OpSetSystemAudioMode, mode.operand())
}

// SystemAudioModeStatus is the audio system's answer to Give System
// Audio Mode Status.
func SystemAudioModeStatus(from, to LogicalAddress, mode SystemAudioMode) Message {
	return NewMessage(from, to, OpSystemAudioModeStatus, mode.operand())
}

// ReadSystemAudioMode asks the audio system for the mode once, and
// records the answer in the directory. It answers SystemAudioUnknown
// when the audio system did not answer or answered with a Feature
// Abort, with the Result so a caller can say which. An error means the
// kernel refused the call.
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

// PressVolumeKey sends one press of a volume key to the audio system:
// User Control Pressed, and then User Control Released at once, so the
// audio system takes one step. HDMI-CEC 1.3a, CEC 13.13.2, says the
// sender of a press sends the release when the button is released, and
// that a follower that hears no release for 500 ms treats the button as
// released. It answers the press's Result, and it sends no release for
// a press the audio system did not acknowledge. An error means the
// kernel refused a call.
func PressVolumeKey(device *Device, own LogicalAddress, key VolumeKey) (Result, error) {
	switch key {
	case KeyVolumeUp, KeyVolumeDown, KeyMute:
	default:
		return Result{}, fmt.Errorf("pressing a volume key: 0x%02x is no volume key", byte(key))
	}
	pressed, err := device.Transmit(NewMessage(own, AddressAudioSystem, OpUserControlPressed, byte(key)), 0, 0)
	if err != nil || !pressed.Acked {
		return pressed, err
	}
	_, err = device.Transmit(NewMessage(own, AddressAudioSystem, OpUserControlReleased), 0, 0)
	return pressed, err
}
