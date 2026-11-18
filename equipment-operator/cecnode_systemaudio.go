package main

// The wake ends with a System Audio Mode Request when the session's
// Display connects through a receiver (HDMI-CEC 1.3a, CEC 13.15.2).
// While System Audio Mode is on, the TV mutes its own speakers and
// sends its remote's volume keys to the receiver. A receiver that wakes
// from standby, for example through its network block, starts with the
// mode off. The TV remote's volume keys then change the volume of the
// TV's speakers, which are not playing the sound.
//
// The request also switches the receiver to the input for the Display's
// physical address, and can wake the receiver. So the adapter sends it
// once for each wokeAt, never on a timer or for a home press, and does
// not send it again when the mode turns off. Sending it again would
// take the sound away from the TV speakers when a person chose them.
// The adapter never sends the request with 0.0.0.0, which asks for the
// TV's own sound, or with no operand, which ends the mode.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// cecSystemAudioWait is how long requestSystemAudio waits for the
// receiver to broadcast Set System Audio Mode On before it reads the
// mode itself. A receiver sends its first On to the TV alone, and
// broadcasts it only when the TV accepts it, so the adapter can hear no
// On even when the mode turns on.
const cecSystemAudioWait = 5 * time.Second

// viaOf returns the Receiver that a Display's picture passes through,
// from the Television's status.displays, or nil. The Deployment sets
// via only when the bus has an audio system above the Display in the
// HDMI tree, so a non-nil result means that the wake sends the request.
func viaOf(television *Television, display string) *EquipmentRef {
	for _, listed := range television.Status.Displays {
		if listed.Name == display && listed.Via != nil && listed.Via.Kind == "Receiver" {
			return listed.Via
		}
	}
	return nil
}

// noteSystemAudio passes a broadcast Set System Audio Mode, or a
// Feature Abort of the request sent to this adapter, to the
// requestSystemAudio that is waiting. With no request waiting it drops
// the message, because an On at another time, such as the reply to the
// TV's own request when the TV turns on, does not answer this adapter.
func (n *cecNode) noteSystemAudio(message cec.Message, own cec.LogicalAddress) {
	opcode, _ := message.Opcode()
	operands := message.Operands()
	statement := opcode == cec.OpSetSystemAudioMode && message.IsBroadcast()
	refusal := opcode == cec.OpFeatureAbort && message.To == own && len(operands) > 0 && cec.Opcode(operands[0]) == cec.OpSystemAudioModeRequest
	if message.From != cec.AddressAudioSystem || !(statement || refusal) {
		return
	}
	n.mutex.Lock()
	waiting := n.audioAnswers
	n.mutex.Unlock()
	if waiting == nil {
		return
	}
	select {
	case waiting <- message:
	default:
	}
}

// audioResult is the outcome of the request. verdict is for the
// SystemAudioApplied condition, and stopped is true when the wake
// stopped or the adapter left before the outcome was known.
type audioResult struct {
	verdict verdict
	log     string
	stopped bool
}

// requestSystemAudio sends System Audio Mode Request for the Display's
// physical address to the receiver. A broadcast of Set System Audio
// Mode On confirms the mode, and a Feature Abort of the request means
// the receiver refused it. When neither arrives within
// cecSystemAudioWait, readSystemAudio reads the mode once.
func (n *cecNode) requestSystemAudio(ctx context.Context, own cec.LogicalAddress, physical cec.PhysicalAddress, display string, via *EquipmentRef) audioResult {
	answers := make(chan cec.Message, 4)
	n.mutex.Lock()
	n.audioAnswers = answers
	n.mutex.Unlock()
	defer func() {
		n.mutex.Lock()
		n.audioAnswers = nil
		n.mutex.Unlock()
	}()
	sent := fmt.Sprintf("the adapter on %s sent System Audio Mode Request for %s, Display %s, to Receiver %s", n.machine, physical, display, via.Name)
	result, err := n.device.Transmit(cec.SystemAudioModeRequest(own, physical), 0, 0)
	switch {
	case err != nil && cec.IsGone(err):
		n.fail(err)
		return audioResult{stopped: true, log: fmt.Sprintf("the adapter on %s was removed while it sent System Audio Mode Request: %v", n.machine, err)}
	case err != nil:
		return refusedAudio(fmt.Sprintf("the adapter on %s did not send System Audio Mode Request for %s, Display %s, to Receiver %s: %v", n.machine, physical, display, via.Name, err))
	case !result.Acked:
		return refusedAudio(fmt.Sprintf("the adapter on %s did not send System Audio Mode Request for %s, Display %s, to Receiver %s: the wire answered %s", n.machine, physical, display, via.Name, result.Status))
	}
	began := time.Now()
	wait := time.NewTimer(cecSystemAudioWait)
	defer wait.Stop()
	for {
		select {
		case <-ctx.Done():
			return audioResult{stopped: true, log: sent + fmt.Sprintf(", and the wake stopped after %s, before the receiver answered", elapsed(time.Since(began)))}
		case <-wait.C:
			return n.readSystemAudio(own, sent)
		case answer := <-answers:
			if opcode, _ := answer.Opcode(); opcode == cec.OpFeatureAbort {
				return refusedAudio(sent + fmt.Sprintf(", and the receiver answered with a Feature Abort after %s", elapsed(time.Since(began))))
			}
			if operands := answer.Operands(); len(operands) > 0 && operands[0] == 1 {
				message := sent + fmt.Sprintf(", and the receiver broadcast Set System Audio Mode On after %s", elapsed(time.Since(began)))
				return audioResult{verdict: verdict{ConditionTrue, reasonConfirmed, message}, log: message}
			}
		}
	}
}

func refusedAudio(message string) audioResult {
	return audioResult{verdict: verdict{ConditionFalse, reasonRefused, message}, log: message}
}

// readSystemAudio reads the mode with Give System Audio Mode Status.
// Only On confirms the mode. ReadSystemAudioMode records the reply in
// the directory, and markDirty makes the node workload write the
// adapter's entry again, from which the Deployment writes the
// Receiver's status.cec.
func (n *cecNode) readSystemAudio(own cec.LogicalAddress, sent string) audioResult {
	sent += fmt.Sprintf(", and heard no broadcast Set System Audio Mode On in the %s after", elapsed(cecSystemAudioWait))
	mode, result, err := cec.ReadSystemAudioMode(n.device, n.directory, own)
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return audioResult{stopped: true, log: fmt.Sprintf("%s; the adapter on %s was removed while it sent Give System Audio Mode Status: %v", sent, n.machine, err)}
	}
	n.markDirty()
	var words string
	switch {
	case err != nil:
		words = fmt.Sprintf("the adapter on %s did not send Give System Audio Mode Status: %v", n.machine, err)
	case mode == cec.SystemAudioOn:
		message := sent + "; the receiver answered Give System Audio Mode Status with On"
		return audioResult{verdict: verdict{ConditionTrue, reasonConfirmed, message}, log: message}
	case mode == cec.SystemAudioOff:
		words = "the receiver answered Give System Audio Mode Status with Off; a TV that does not take System Audio Mode refuses the receiver's On, and the receiver stays Off"
	case result.Aborted:
		words = "the receiver answered Give System Audio Mode Status with a Feature Abort"
	default:
		words = "the receiver did not answer Give System Audio Mode Status, which a receiver with its HDMI control off does not answer"
	}
	message := sent + "; " + words
	return audioResult{verdict: verdict{ConditionFalse, reasonUnconfirmed, message}, log: message}
}
