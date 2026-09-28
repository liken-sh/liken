package cec

// Reading the bus, and the answers a follower owes.

import (
	"context"
	"errors"
	"time"
)

// waitSlice bounds each wait of the read loop, so the loop sees a
// cancelled context within this time even on a silent bus.
const waitSlice = 250 * time.Millisecond

// Read passes every message and every event the kernel queues for this
// handle to the two functions, until ctx ends or a call fails. The
// error of a call that fails is returned; IsGone reports whether it
// means the adapter left.
func Read(ctx context.Context, device *Device, message func(Message), event func(Event)) error {
	for ctx.Err() == nil {
		ready, err := device.Wait(waitSlice)
		if err != nil {
			return err
		}
		if ready.Event {
			happened, err := device.NextEvent()
			if err != nil {
				return err
			}
			event(happened)
		}
		if ready.Message {
			received, err := device.Receive(time.Millisecond)
			if errors.Is(err, ErrNothingReceived) {
				continue
			}
			if err != nil {
				return err
			}
			message(received)
		}
	}
	return nil
}

// answers are the opcodes that answer a request. A follower receives
// them when a reply arrives after its request timed out, and it must
// not answer an answer with a Feature Abort. The kernel also passes
// the remote's User Control messages to a follower, and those are not
// requests either.
var answers = map[Opcode]bool{
	OpFeatureAbort:          true,
	OpReportPhysicalAddr:    true,
	OpSetOSDName:            true,
	OpDeviceVendorID:        true,
	OpCECVersion:            true,
	OpReportPowerStatus:     true,
	OpUserControlPressed:    true,
	OpUserControlReleased:   true,
	OpDeckStatus:            true,
	OpTunerDeviceStatus:     true,
	OpMenuStatus:            true,
	OpSetSystemAudioMode:    true,
	OpSystemAudioModeStatus: true,
	OpReportAudioStatus:     true,
	OpReportShortAudioDesc:  true,
	OpReportFeatures:        true,
	OpReportCurrentLatency:  true,
	OpReportArcInitiated:    true,
	OpReportArcTerminated:   true,
	OpActiveSource:          true,
	OpInactiveSource:        true,
	OpSetMenuLanguage:       true,
	OpRoutingChange:         true,
	OpRoutingInformation:    true,
	OpSetStreamPath:         true,
	OpStandby:               true,
}

// Answer is the reply a follower at address own sends to one received
// message, and false when it sends none. The kernel sends a Feature
// Abort for an unsupported message only when no follower exists, so a
// follower owes one for every directed request it does not support.
// It answers one request itself: Give Device Power Status, with the
// power the caller states, because the kernel does not answer it.
//
// A message from the unregistered address gets no answer. An answer
// to address 15 would be a broadcast, and a Feature Abort and a Report
// Power Status are directed messages.
func Answer(message Message, own LogicalAddress, power PowerStatus) (Message, bool) {
	if message.IsBroadcast() || message.IsPoll() || message.To != own || message.From == own ||
		message.From == AddressUnregistered {
		return Message{}, false
	}
	opcode, _ := message.Opcode()
	switch {
	case opcode == OpGiveDevicePowerStatus:
		return ReportPowerStatus(own, message.From, power), true
	case answers[opcode]:
		return Message{}, false
	}
	return FeatureAbort(own, message.From, opcode, AbortUnrecognizedOpcode), true
}
