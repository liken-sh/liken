package main

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
)

func reachableReasonAndMessage(status ConditionStatus) (reason, message string) {
	switch status {
	case ConditionTrue:
		return reasonConnected, "the receiver answered"
	case ConditionFalse:
		return reasonUnreachable, "the receiver did not answer"
	}
	return reasonConnecting, "the operator has not reached the receiver yet"
}

// timestamp formats a time as RFC 3339 in UTC, the form the API server
// stores.
func timestamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

// reachable builds the Reachable condition. It keeps the previous
// lastTransitionTime unless the status changed.
func reachable(status ConditionStatus, generation int64, previous []Condition, now time.Time) Condition {
	reason, message := reachableReasonAndMessage(status)
	return withTransitionTime(previous, Condition{
		Type:               reachableConditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	}, now)
}

// settingsConfirmed builds the SettingsConfirmed condition, which lists
// the fields the operator stopped sending after sendLimit sends. It
// returns false when there are none, and the status then leaves the
// condition out.
func settingsConfirmed(unconfirmed []string, generation int64, previous []Condition, now time.Time) (Condition, bool) {
	if len(unconfirmed) == 0 {
		return Condition{}, false
	}
	return withTransitionTime(previous, Condition{
		Type:               settingsConfirmedConditionType,
		Status:             ConditionFalse,
		ObservedGeneration: generation,
		Reason:             reasonNotConfirmed,
		Message:            fmt.Sprintf("the receiver did not report the declared value after %d sends: %s", sendLimit, strings.Join(unconfirmed, ", ")),
	}, now), true
}

// buildReceiverStatus builds a receiver's status from its state, in the
// receiver's own units, with the Denon settings or the WiiM status of
// the receiver's protocol.
func buildReceiverStatus(state equipment.State, settings *denon.Settings, wiimStatus *wiim.Status, address string, resolution int, generation int64, previous []Condition, now time.Time) ReceiverStatus {
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
		Address:      address,
		Model:        state.Model,
		Manufacturer: state.Manufacturer,
		Zones:        zones,
		Driver:       protocolName(settings, wiimStatus),
		Denon:        settings,
		Wiim:         wiimStatus,
		Conditions:   []Condition{reachable(state.Reachable, generation, previous, now)},
	}
}

// protocolName returns the value of status.driver.
func protocolName(settings *denon.Settings, wiimStatus *wiim.Status) string {
	switch {
	case wiimStatus != nil:
		return "wiim"
	case settings != nil:
		return "denon"
	}
	return ""
}

// sleepMinutes reports a timer that the driver has not read, a negative
// value, as 0, which means off.
func sleepMinutes(minutes int) int {
	if minutes < 0 {
		return 0
	}
	return minutes
}

// sameStatus reports whether writing b over a would change nothing.
func sameStatus(a, b ReceiverStatus) bool {
	if a.Driver != b.Driver || a.Model != b.Model || a.Manufacturer != b.Manufacturer || a.SettledPower != b.SettledPower || !maps.Equal(a.SettledSettings, b.SettledSettings) || !reflect.DeepEqual(a.Denon, b.Denon) ||
		!reflect.DeepEqual(a.Wiim, b.Wiim) ||
		len(a.Zones) != len(b.Zones) || len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for name, zone := range a.Zones {
		other, found := b.Zones[name]
		if !found || zone != other {
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
