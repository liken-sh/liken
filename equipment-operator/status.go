package main

// The status one receiver reports: what it last said, in its own units,
// and the one condition that says whether the operator can still reach
// it.

import (
	"reflect"
	"time"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
)

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
// in the receiver's own units, plus the protocol's own typed settings
// snapshot. status.service stays empty until the operator makes the
// Service front.
func buildReceiverStatus(state equipment.State, settings *denon.Settings, resolution int, generation int64, previous []Condition, now time.Time) ReceiverStatus {
	zones := make(map[string]ZoneStatus, len(state.Zones))
	for name, zone := range state.Zones {
		zones[name] = ZoneStatus{
			Power:     string(zone.Power),
			Input:     zone.Input,
			SoundMode: zone.SoundMode,
			Mute:      zone.Mute,
			Volume:    formatSteps(zone.Volume, resolution),
			VolumeMax: formatSteps(zone.VolumeMax, resolution),
			Sleep:     sleepMinutes(zone.Sleep),
		}
	}
	return ReceiverStatus{
		Zones:      zones,
		Denon:      settings,
		Conditions: []Condition{reachable(state.Reachable, generation, previous, now)},
	}
}

// sleepMinutes writes one zone's sleep timer the way the status carries
// it. A driver that has not reported the timer reads as off, because a
// zero the receiver never said would read as a real answer.
func sleepMinutes(minutes int) int {
	if minutes < 0 {
		return 0
	}
	return minutes
}

// sameStatus answers whether a write would change anything.
func sameStatus(a, b ReceiverStatus) bool {
	if a.Service != b.Service || !reflect.DeepEqual(a.Denon, b.Denon) ||
		len(a.Zones) != len(b.Zones) || len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for name, zone := range a.Zones {
		other, held := b.Zones[name]
		if !held || zone != other {
			return false
		}
	}
	for index := range a.Conditions {
		if a.Conditions[index] != b.Conditions[index] {
			return false
		}
	}
	return true
}
