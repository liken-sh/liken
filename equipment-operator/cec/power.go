package cec

// The TV's power: the command that asks the TV for a state, and the
// read that confirms the state. The node workload sends the command
// once per change of a Television's spec.power and reads the power
// until the TV reports the state it asked for.

// PowerCommand is the generic message that asks the TV to wake, or to
// go to standby. Image View On wakes a TV from standby. Standby
// directed to the TV puts the TV alone in standby, and the receiver
// and the other sources stay as they are; a broadcast Standby would
// put every device on the bus in standby.
//
// Image View On also asks the TV to show a source, and a TV can switch
// to the sender's input when it wakes. The adapter announces its
// Display's physical address, so that input is the machine's.
//
// The order is generic. A TV brand that needs another order gets a
// handler here, taken from libCEC's handler for that brand, as
// AGENTS.md states.
func PowerCommand(from LogicalAddress, on bool) Message {
	if on {
		return ImageViewOn(from, AddressTV)
	}
	return Standby(from, AddressTV)
}

// ReadPower asks one device for its power status and records what the
// wire answers in the directory, by the same rules a scan follows. A
// device that acknowledges and does not answer, or answers with a
// Feature Abort, has no power this read can state. After two such
// reads in a row the directory forgets the power it held: a TV in a
// deep standby can stop answering, and its last answer is then no
// longer true. One missed reply keeps the power, because a TV that
// wakes can miss one question. A NACK means
// no device holds the address. A lost arbitration or a wire error says
// nothing about the device, so the directory keeps what it held. An
// error means the kernel refused the call.
func ReadPower(device *Device, directory *Directory, own, to LogicalAddress) (PowerStatus, error) {
	result, err := device.Transmit(GiveDevicePowerStatus(own, to), OpReportPowerStatus, replyTimeout)
	if err != nil {
		return PowerUnknown, err
	}
	switch {
	case result.Nacked:
		directory.Forget(to)
	case !result.Acked:
	case result.Reply == nil:
		directory.Unanswered(to)
	default:
		directory.Observe(*result.Reply)
		if operands := result.Reply.Operands(); len(operands) > 0 {
			return PowerStatus(operands[0]), nil
		}
	}
	return PowerUnknown, nil
}
