package main

// The System Audio Mode Request in the wake (HDMI-CEC 1.3a, CEC
// 13.15.2). While System Audio Mode is on, the TV mutes its own
// speakers and sends its remote's volume keys to the receiver. A
// receiver that the network block wakes comes up with the mode off, so
// without the request the TV remote's volume keys change the TV's own
// volume, which the room does not hear. So when the session's Display
// passes through a receiver, the wake asks the receiver for the mode
// once, with the Display's physical address.
//
// The request also switches the receiver to the input for that
// address and can wake it, so the request is a step of the wake and
// never a setting the operator converges to: a request sent again on
// drift would switch the receiver's input against a person who picked
// the TV's speakers. The adapter sends it once for each wokeAt, never
// on a timer, and never for a home press. It never sends the request
// with 0.0.0.0, which asks for the TV's own sound, or with no operand,
// which ends the mode.

import (
	"context"
	"fmt"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// cecSystemAudioWait is how long the adapter waits for the receiver's
// broadcast Set System Audio Mode [On] after the request. The receiver
// directs its first [On] to the TV and broadcasts it only when the TV
// takes it, so an adapter in Control can miss the first one, and after
// the wait the adapter reads the mode once instead.
const cecSystemAudioWait = 5 * time.Second

// viaOf answers the Receiver that a Display's picture passes through,
// as the Deployment lists it in the Television's status.displays, and
// nil when it passes through none. The Deployment writes a via only
// when the bus has an audio system above the Display in the HDMI tree,
// so a via is the condition for the request.
func viaOf(television *Television, display string) *EquipmentRef {
	for _, listed := range television.Status.Displays {
		if listed.Name == display && listed.Via != nil && listed.Via.Kind == "Receiver" {
			return listed.Via
		}
	}
	return nil
}

// noteSystemAudio passes the receiver's answers to a request that
// waits: a broadcast Set System Audio Mode, and a Feature Abort of the
// request to this adapter. A message that arrives while no request
// waits, such as the [On] that answers the TV's own request as it comes
// on, answers no request of this adapter's.
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

// audioResult is what the request did: the verdict of
// SystemAudioApplied, the line the log states, and whether the wake
// stopped before the receiver answered.
type audioResult struct {
	verdict verdict
	log     string
	stopped bool
}

// requestSystemAudio sends System Audio Mode Request for the Display's
// physical address to the receiver and states what the receiver
// answered. A broadcast [On] after the request confirms the mode. A
// Feature Abort of the request refuses it. With neither in
// cecSystemAudioWait, the adapter asks for the mode once: On confirms
// it, and Off or no answer leaves it unconfirmed.
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
		return audioResult{stopped: true, log: fmt.Sprintf("the adapter on %s left while it sent System Audio Mode Request: %v", n.machine, err)}
	case err != nil:
		return refusedAudio(fmt.Sprintf("the adapter on %s could not send System Audio Mode Request for %s, Display %s, to Receiver %s: %v", n.machine, physical, display, via.Name, err))
	case !result.Acked:
		return refusedAudio(fmt.Sprintf("the adapter on %s could not send System Audio Mode Request for %s, Display %s, to Receiver %s: the wire answered %s", n.machine, physical, display, via.Name, result.Status))
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

// refusedAudio is the result of a request the adapter could not send
// or the receiver refused.
func refusedAudio(message string) audioResult {
	return audioResult{verdict: verdict{ConditionFalse, reasonRefused, message}, log: message}
}

// readSystemAudio asks the receiver for the mode once, after a request
// that no broadcast [On] answered, and records the answer in the
// directory, so the entry and the Receiver's status.cec follow it.
func (n *cecNode) readSystemAudio(own cec.LogicalAddress, sent string) audioResult {
	sent += fmt.Sprintf(", and heard no broadcast Set System Audio Mode On in the %s after", elapsed(cecSystemAudioWait))
	mode, result, err := cec.ReadSystemAudioMode(n.device, n.directory, own)
	if err != nil && cec.IsGone(err) {
		n.fail(err)
		return audioResult{stopped: true, log: fmt.Sprintf("%s; the adapter on %s left while it sent Give System Audio Mode Status: %v", sent, n.machine, err)}
	}
	n.markDirty()
	var words string
	switch {
	case err != nil:
		words = fmt.Sprintf("the adapter on %s could not send Give System Audio Mode Status: %v", n.machine, err)
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
