package cec

import (
	"context"
	"errors"
	"time"
)

// Read waits at most waitSlice at a time, so that it returns soon after
// ctx is canceled even when the bus is silent.
const waitSlice = 250 * time.Millisecond

// Read calls message for each message and event for each event that the
// kernel queues for this handle, until ctx ends or a call fails. It
// returns the error of the failed call, and IsGone reports whether that
// error means the adapter was removed.
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

// answers is the set of opcodes that are replies or notifications, not
// requests. A follower can receive a reply after its own request timed
// out, and it must not send a Feature Abort for it. The kernel also
// passes the remote's User Control messages to a follower, and those are
// not requests either.
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

// Answer returns the reply that a follower at logical address own sends
// to message, and false when it sends none. The kernel sends a Feature
// Abort for an unsupported message only when no follower is registered,
// so a follower must send one for each directed request it does not
// support. The only request that gets a reply with content is Give
// Device Power Status, because the kernel does not reply to it.
//
// audioSystem is true when the bus has a device at logical address 5.
// The node workload sends System Audio Mode Request only to that device.
// HDMI-CEC 1.3a, CEC Table 25, says that a device that sends the request
// shall not Feature Abort Set System Audio Mode or System Audio Mode
// Status. CEC Table 24 says that a device that accepts Set System Audio
// Mode shall be able to send the request. So on a bus with no audio
// system, a directed Set System Audio Mode or System Audio Mode Status
// gets a Feature Abort. A broadcast gets none (CEC 12.3).
//
// A message from the unregistered address, 15, gets no reply, because a
// reply to address 15 would be a broadcast, and Feature Abort and Report
// Power Status are directed messages.
func Answer(message Message, own LogicalAddress, power PowerStatus, audioSystem bool) (Message, bool) {
	if message.IsBroadcast() || message.IsPoll() || message.To != own || message.From == own ||
		message.From == AddressUnregistered {
		return Message{}, false
	}
	opcode, _ := message.Opcode()
	systemAudio := opcode == OpSetSystemAudioMode || opcode == OpSystemAudioModeStatus
	switch {
	case opcode == OpGiveDevicePowerStatus:
		return ReportPowerStatus(own, message.From, power), true
	case systemAudio && !audioSystem:
	case answers[opcode]:
		return Message{}, false
	}
	return FeatureAbort(own, message.From, opcode, AbortUnrecognizedOpcode), true
}
