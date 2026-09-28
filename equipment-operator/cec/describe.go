package cec

// The words for the messages a person notices on the bus: a TV that
// wakes or goes to standby, an input that changes, the sound that moves
// to the receiver, and a button on the TV's remote. A log line names
// each one, so a person can find which device changed the TV's input.
// The operand layouts are those of linux/cec-funcs.h.

import "fmt"

// visible names each message that changes what a person sees or hears.
// Every other message, such as a poll or an identity question, is
// traffic between devices.
var visible = map[Opcode]string{
	OpActiveSource:           "Active Source",
	OpInactiveSource:         "Inactive Source",
	OpRequestActiveSource:    "Request Active Source",
	OpRoutingChange:          "Routing Change",
	OpRoutingInformation:     "Routing Information",
	OpSetStreamPath:          "Set Stream Path",
	OpImageViewOn:            "Image View On",
	OpTextViewOn:             "Text View On",
	OpStandby:                "Standby",
	OpSetSystemAudioMode:     "Set System Audio Mode",
	OpSystemAudioModeRequest: "System Audio Mode Request",
	OpReportPowerStatus:      "Report Power Status",
	OpUserControlPressed:     "User Control Pressed",
}

// Describe writes a visible message as a person reads it: the
// message's name and its operands decoded. It answers false for a
// message that is not visible. A message with fewer operand bytes than
// its layout needs is named with the bytes it has, because the monitor
// passes such a message on and the kernel drops it for a follower.
func Describe(message Message) (string, bool) {
	opcode, found := message.Opcode()
	name := visible[opcode]
	if !found || name == "" {
		return "", false
	}
	operands := message.Operands()
	if len(operands) < operandBytes(opcode) {
		return fmt.Sprintf("%s with %d operand bytes, too few to read: % x", name, len(operands), operands), true
	}
	switch opcode {
	case OpActiveSource, OpInactiveSource, OpRoutingInformation, OpSetStreamPath:
		return fmt.Sprintf("%s %s", name, physicalAt(operands, 0)), true
	case OpRoutingChange:
		return fmt.Sprintf("%s from %s to %s", name, physicalAt(operands, 0), physicalAt(operands, 2)), true
	case OpSetSystemAudioMode:
		return fmt.Sprintf("%s %s", name, audioMode(operands[0])), true
	case OpSystemAudioModeRequest:
		// The request with no physical address asks the audio system to
		// turn System Audio Mode off.
		if len(operands) < 2 {
			return name + " with no physical address, which asks for Off", true
		}
		return fmt.Sprintf("%s for %s", name, physicalAt(operands, 0)), true
	case OpReportPowerStatus:
		power := PowerStatus(operands[0]).String()
		if power == "" {
			power = fmt.Sprintf("0x%02x, which CEC does not define", operands[0])
		}
		return fmt.Sprintf("%s %s", name, power), true
	case OpUserControlPressed:
		return fmt.Sprintf("%s %s", name, keyName(operands[0])), true
	}
	return name, true
}

// operandBytes is the fewest operand bytes a visible message needs.
func operandBytes(opcode Opcode) int {
	switch opcode {
	case OpActiveSource, OpInactiveSource, OpRoutingInformation, OpSetStreamPath:
		return 2
	case OpRoutingChange:
		return 4
	case OpSetSystemAudioMode, OpReportPowerStatus, OpUserControlPressed:
		return 1
	}
	return 0
}

// physicalAt reads the physical address in two operand bytes, high byte
// first.
func physicalAt(operands []byte, at int) PhysicalAddress {
	return PhysicalAddress(operands[at])<<8 | PhysicalAddress(operands[at+1])
}

// audioMode names the operand of Set System Audio Mode.
func audioMode(status byte) string {
	switch status {
	case 0:
		return "Off"
	case 1:
		return "On"
	}
	return fmt.Sprintf("0x%02x, which CEC does not define", status)
}

// keyNames are the UI command codes of User Control Pressed, from the
// CEC_OP_UI_CMD_ values in linux/cec.h.
var keyNames = map[byte]string{
	0x00: "Select", 0x01: "Up", 0x02: "Down", 0x03: "Left", 0x04: "Right",
	0x05: "Right Up", 0x06: "Right Down", 0x07: "Left Up", 0x08: "Left Down",
	0x09: "Device Root Menu", 0x0a: "Device Setup Menu", 0x0b: "Contents Menu",
	0x0c: "Favorite Menu", 0x0d: "Back",
	0x10: "Media Top Menu", 0x11: "Media Context-Sensitive Menu",
	0x1d: "Number Entry Mode", 0x1e: "Number 11", 0x1f: "Number 12",
	0x20: "Number 0 or Number 10", 0x21: "Number 1", 0x22: "Number 2",
	0x23: "Number 3", 0x24: "Number 4", 0x25: "Number 5", 0x26: "Number 6",
	0x27: "Number 7", 0x28: "Number 8", 0x29: "Number 9",
	0x2a: "Dot", 0x2b: "Enter", 0x2c: "Clear", 0x2f: "Next Favorite",
	0x30: "Channel Up", 0x31: "Channel Down", 0x32: "Previous Channel",
	0x33: "Sound Select", 0x34: "Input Select", 0x35: "Display Information",
	0x36: "Help", 0x37: "Page Up", 0x38: "Page Down",
	0x40: "Power", 0x41: "Volume Up", 0x42: "Volume Down", 0x43: "Mute",
	0x44: "Play", 0x45: "Stop", 0x46: "Pause", 0x47: "Record",
	0x48: "Rewind", 0x49: "Fast Forward", 0x4a: "Eject",
	0x4b: "Skip Forward", 0x4c: "Skip Backward", 0x4d: "Stop Record",
	0x4e: "Pause Record",
	0x50: "Angle", 0x51: "Sub Picture", 0x52: "Video On Demand",
	0x53: "Electronic Program Guide", 0x54: "Timer Programming",
	0x55: "Initial Configuration", 0x56: "Select Broadcast Type",
	0x57: "Select Sound Presentation", 0x58: "Audio Description",
	0x59: "Internet", 0x5a: "3D Mode",
	0x60: "Play Function", 0x61: "Pause-Play Function", 0x62: "Record Function",
	0x63: "Pause-Record Function", 0x64: "Stop Function", 0x65: "Mute Function",
	0x66: "Restore Volume Function", 0x67: "Tune Function",
	0x68: "Select Media Function", 0x69: "Select AV Input Function",
	0x6a: "Select Audio Input Function", 0x6b: "Power Toggle Function",
	0x6c: "Power Off Function", 0x6d: "Power On Function",
	0x71: "F1 (Blue)", 0x72: "F2 (Red)", 0x73: "F3 (Green)", 0x74: "F4 (Yellow)",
	0x75: "F5", 0x76: "Data",
}

// keyName names a UI command, and gives the code of one linux/cec.h
// does not list.
func keyName(code byte) string {
	if name, found := keyNames[code]; found {
		return name
	}
	return fmt.Sprintf("key 0x%02x", code)
}
