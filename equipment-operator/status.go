package main

// The status one receiver reports: what it last said, in its own units,
// and the one condition that says whether the operator can still reach
// it.

import "time"

// The one condition this operator reports, and the reason for each
// verdict.
const (
	reachableConditionType = "Reachable"
	reasonConnected        = "Connected"
	reasonUnreachable      = "Unreachable"
	reasonConnecting       = "Connecting"
)

// reachableWords is the reason and the message each verdict carries.
func reachableWords(status ConditionStatus) (reason, message string) {
	switch status {
	case ConditionTrue:
		return reasonConnected, "the receiver answered"
	case ConditionFalse:
		return reasonUnreachable, "the receiver did not answer"
	}
	return reasonConnecting, "the operator has not reached the receiver yet"
}

// timestamp writes a moment the way the API server holds one.
func timestamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

// reachable builds the condition. It keeps the moment the verdict last
// changed, so the stamp moves only when the verdict flips.
func reachable(status ConditionStatus, generation int64, previous []Condition, now time.Time) Condition {
	reason, message := reachableWords(status)
	condition := Condition{
		Type:               reachableConditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: timestamp(now),
	}
	for _, held := range previous {
		if held.Type == reachableConditionType && held.Status == status && held.LastTransitionTime != "" {
			condition.LastTransitionTime = held.LastTransitionTime
		}
	}
	return condition
}

// buildReceiverStatus is the whole status one receiver's state makes,
// in the receiver's own units. status.service stays empty until the
// operator makes the Service front.
func buildReceiverStatus(state denonState, generation int64, previous []Condition, now time.Time) ReceiverStatus {
	return ReceiverStatus{
		Power:      state.Power,
		Input:      state.Input,
		Volume:     formatHalfSteps(state.Volume),
		VolumeMax:  formatHalfSteps(state.VolumeMax),
		Mute:       state.Mute,
		SoundMode:  state.SoundMode,
		Conditions: []Condition{reachable(state.Reachable, generation, previous, now)},
	}
}

// sameStatus answers whether a write would change anything.
func sameStatus(a, b ReceiverStatus) bool {
	if a.Power != b.Power || a.Input != b.Input || a.Volume != b.Volume ||
		a.VolumeMax != b.VolumeMax || a.Mute != b.Mute ||
		a.SoundMode != b.SoundMode || a.Service != b.Service ||
		len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for index := range a.Conditions {
		if a.Conditions[index] != b.Conditions[index] {
			return false
		}
	}
	return true
}
