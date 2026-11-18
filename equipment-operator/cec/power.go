package cec

// PowerCommand returns Image View On to wake the TV, or Standby to put it
// in standby. Standby goes to the TV alone, because a broadcast Standby
// would also put the receiver and the other sources in standby. A TV
// can switch to the sender's input when Image View On wakes it.
//
// Every TV brand gets the same messages. cec/AGENTS.md gives the rule
// for adding a brand that needs others.
func PowerCommand(from LogicalAddress, on bool) Message {
	if on {
		return ImageViewOn(from, AddressTV)
	}
	return Standby(from, AddressTV)
}

// ReadPower sends Give Device Power Status to one device, records the
// result in the directory, and returns the power status from the reply,
// or PowerUnknown when there is none. It returns an error only when the
// kernel refuses the call.
//
// A NACK means no device has the address, so the directory forgets the
// peer. An acknowledged message with no reply, or with a Feature Abort,
// counts as a missed reply, and Directory.Unanswered clears the power
// after missedReplies in a row, because a TV in deep standby can stop
// replying. A send that ends with neither an acknowledgement nor a
// NACK, as after a lost arbitration or a wire error, tells nothing
// about the device, so the directory keeps what it has.
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
