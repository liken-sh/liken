package main

// This file derives the status of one CECBus from the entries that the
// node workloads wrote: one merged device list for the wire, and the
// conditions that say whether the adapters did what spec.mode asks.
// Each node workload reports only what its own adapter sees, so the
// merge has to happen in the operator that reads every entry.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// verdict is the status, reason, and message of one condition, before
// stampCondition adds the generation and the transition time.
type verdict struct {
	status  ConditionStatus
	reason  string
	message string
}

// deriveCECBus returns the merged device list and the conditions of one
// bus. It ignores entries of machines that spec.adapters does not name,
// because an entry of a machine that was removed from the spec
// describes another wire. live is the readiness of the node workload's
// pods (cecpods.go), and now is the transition time of a condition
// whose status changes.
func deriveCECBus(bus *CECBus, live podLiveness, now time.Time) ([]CECDevice, []Condition) {
	entries := reportedEntries(bus, live)
	verdicts := []struct {
		kind string
		verdict
	}{
		{conditionAddressKnown, addressKnown(bus.Spec, entries)},
		{conditionJoined, joined(bus.Spec, entries)},
		{conditionCoherent, coherent(bus.Spec, entries)},
		{conditionScanned, scanned(bus.Spec, entries)},
	}
	conditions := make([]Condition, 0, len(verdicts))
	for _, each := range verdicts {
		conditions = append(conditions, stampCondition(each.kind, each.verdict, bus.Metadata.Generation, bus.Status.Conditions, now))
	}
	return mergeDevices(bus.Spec, entries), conditions
}

// reports has the entries of the machines that the spec names. current
// has each entry that still describes its adapter. gone has the verdict
// for each entry that does not, because it is stale or the adapter
// stopped. recheck is the earliest time at which a current entry becomes
// stale with no watch event to start another pass: the end of the
// startup grace of a pod that is not Ready yet. It is zero when no entry
// becomes stale that way.
type reports struct {
	current map[string]CECAdapterStatus
	gone    map[string]verdict
	recheck time.Time
}

// reportedEntries splits the entries of the machines that the spec
// names into current and gone. A machine with no entry is in neither.
func reportedEntries(bus *CECBus, live podLiveness) reports {
	sorted := reports{current: map[string]CECAdapterStatus{}, gone: map[string]verdict{}}
	for _, entry := range bus.Status.Adapters {
		if _, named := bus.Spec.names(entry.Machine); !named {
			continue
		}
		replaced, isGone, recheck := goneVerdict(entry, live)
		if isGone {
			sorted.gone[entry.Machine] = replaced
			continue
		}
		sorted.current[entry.Machine] = entry
		sorted.recheck = earliest(sorted.recheck, recheck)
	}
	return sorted
}

// goneVerdict returns the verdict for an entry that no longer describes
// its adapter, and true. For an entry that still does, it returns false
// and the time at which the entry becomes stale, if any. A Stopped
// entry is the last thing the node workload wrote before it exited, so
// it takes precedence over what the pods show.
func goneVerdict(entry CECAdapterStatus, live podLiveness) (verdict, bool, time.Time) {
	if entry.State == AdapterStopped {
		return verdict{ConditionFalse, reasonStopped, fmt.Sprintf("The node workload on %s stopped and released the adapter: %s", entry.Machine, entry.Message)}, true, time.Time{}
	}
	message, stale, recheck := live.stale(entry)
	if stale {
		return verdict{ConditionUnknown, reasonStale, message}, true, time.Time{}
	}
	return verdict{}, false, recheck
}

// earliest returns the earlier of two times, and treats a zero time as
// no time.
func earliest(first, second time.Time) time.Time {
	if first.IsZero() || (!second.IsZero() && second.Before(first)) {
		return second
	}
	return first
}

// lookup returns a machine's current entry, or the verdict that
// replaces it. For a machine with neither, the verdict is notReported
// with the status missing.
func (r reports) lookup(machine string, missing ConditionStatus) (CECAdapterStatus, *verdict) {
	if absent, isGone := r.gone[machine]; isGone {
		return CECAdapterStatus{}, &absent
	}
	if entry, found := r.current[machine]; found {
		return entry, nil
	}
	absent := notReported(machine, missing)
	return CECAdapterStatus{}, &absent
}

// stampCondition builds the condition for a verdict. The transition
// time changes only when the status differs from the previous
// condition of the same type.
func stampCondition(kind string, v verdict, generation int64, previous []Condition, now time.Time) Condition {
	return withTransitionTime(previous, Condition{
		Type:               kind,
		Status:             v.status,
		ObservedGeneration: generation,
		Reason:             v.reason,
		Message:            v.message,
	}, now)
}

// notReported is the verdict for a machine that has no entry. Either
// the machine has no adapter or no node workload pod runs on it.
func notReported(machine string, status ConditionStatus) verdict {
	return verdict{status, reasonNotReported, fmt.Sprintf(
		"No equipment-operator-cec pod reports an adapter on machine %s. Check that the machine publishes a -cec device and that the pod runs there", machine)}
}

// refused is the verdict for an adapter whose node workload reported a
// failed call. Its message includes the entry's message.
func refused(entry CECAdapterStatus) verdict {
	return verdict{ConditionFalse, reasonRefused, fmt.Sprintf("The adapter on %s: %s", entry.Machine, entry.Message)}
}

var listenOnly = verdict{ConditionFalse, reasonListening, "The bus is in Listen, so no adapter claims a logical address or announces a physical address"}

// addressKnown is True when every adapter has a physical address from
// its Display. It applies only in Control.
func addressKnown(spec CECBusSpec, entries reports) verdict {
	if spec.Mode != CECControl {
		return listenOnly
	}
	for _, adapter := range spec.Adapters {
		entry, absent := entries.lookup(adapter.Machine, ConditionFalse)
		switch {
		case absent != nil:
			return *absent
		case entry.PhysicalAddress == "" && entry.Message != "":
			return verdict{ConditionFalse, reasonNoAddress, fmt.Sprintf("The adapter on %s has no physical address: %s", adapter.Machine, entry.Message)}
		case entry.PhysicalAddress == "":
			return verdict{ConditionFalse, reasonNoAddress, fmt.Sprintf("The adapter on %s has no physical address from Display %s", adapter.Machine, adapter.Display)}
		}
	}
	return verdict{ConditionTrue, conditionAddressKnown, "Every adapter announces its Display's physical address"}
}

// joined is True when every adapter holds a logical address. It applies
// only in Control.
func joined(spec CECBusSpec, entries reports) verdict {
	if spec.Mode != CECControl {
		return listenOnly
	}
	for _, adapter := range spec.Adapters {
		entry, absent := entries.lookup(adapter.Machine, ConditionFalse)
		switch {
		case absent != nil:
			return *absent
		case entry.State == AdapterRefused:
			return refused(entry)
		case entry.LogicalAddress == nil:
			return verdict{ConditionFalse, reasonNoLogical, fmt.Sprintf("The adapter on %s has no logical address yet", adapter.Machine)}
		}
	}
	return verdict{ConditionTrue, conditionJoined, "Every adapter has a logical address"}
}

// coherent is True when each adapter that finished a scan found every
// other such adapter at the physical address that adapter announces.
// When one does not, the two adapters are probably on different wires,
// not on the one wire that the spec describes. The check cannot match
// on OSD name, because every adapter of a bus announces the bus's
// spec.osdName. It does not match on logical address, because an
// adapter can claim a different one when it joins again, and its
// physical address, which comes from its Display, stays the same.
func coherent(spec CECBusSpec, entries reports) verdict {
	if spec.Mode != CECControl {
		return verdict{ConditionUnknown, reasonListening, "The bus is in Listen, and an adapter in Listen announces nothing for the others to see"}
	}
	var members []CECAdapterStatus
	for _, adapter := range spec.Adapters {
		if absent, isGone := entries.gone[adapter.Machine]; isGone {
			return absent
		}
		if entry, found := entries.current[adapter.Machine]; found && entry.State == AdapterScanned {
			members = append(members, entry)
		}
	}
	if len(members) < 2 {
		return verdict{ConditionTrue, reasonOneAdapter, "At most one adapter has scanned the bus, so no adapter has another to see"}
	}
	for _, seer := range members {
		for _, seen := range members {
			if seer.Machine != seen.Machine && !sees(seer, seen) {
				return verdict{ConditionFalse, reasonApart, fmt.Sprintf(
					"The adapter on %s does not see the adapter on %s at physical address %s. The two adapters may be on different CEC wires",
					seer.Machine, seen.Machine, seen.PhysicalAddress)}
			}
		}
	}
	return verdict{ConditionTrue, conditionCoherent, "Every adapter sees every other adapter on the bus"}
}

// sees reports whether the seer's devices include a device at the
// physical address that seen announces.
func sees(seer, seen CECAdapterStatus) bool {
	if seen.PhysicalAddress == "" {
		return false
	}
	for _, device := range seer.Devices {
		if device.PhysicalAddress == seen.PhysicalAddress {
			return true
		}
	}
	return false
}

// silentCable ends the message for an adapter that found no device,
// because many HDMI cables do not carry the CEC wire, and an adapter on
// such a cable receives nothing. The message does not name the device
// at the other end of the cable, because the adapter can be on a
// receiver's input or between a machine and a TV.
const silentCable = "The HDMI cable at the adapter's output may not carry the CEC wire"

// scanned is True when the device list is complete. In Control that
// means every adapter finished a scan and found at least one device.
// In Listen it is never True, because a complete list needs polls and
// a poll is a transmission.
func scanned(spec CECBusSpec, entries reports) verdict {
	for _, adapter := range spec.Adapters {
		entry, absent := entries.lookup(adapter.Machine, ConditionUnknown)
		switch {
		case absent != nil:
			return *absent
		case entry.State == AdapterRefused:
			return refused(entry)
		case spec.Mode == CECListen && len(entry.Devices) == 0:
			return verdict{ConditionFalse, reasonSilent, fmt.Sprintf("The adapter on %s has heard no device yet. %s", adapter.Machine, silentCable)}
		case spec.Mode == CECControl && entry.State != AdapterScanned:
			return verdict{ConditionUnknown, reasonScanning, fmt.Sprintf("The adapter on %s has not finished a scan", adapter.Machine)}
		case spec.Mode == CECControl && len(entry.Devices) == 0:
			return verdict{ConditionFalse, reasonNoAnswer, fmt.Sprintf("No device answered the polls of the adapter on %s. %s", adapter.Machine, silentCable)}
		}
	}
	if spec.Mode != CECControl {
		return verdict{ConditionFalse, reasonListening, "The bus is in Listen, so the device list has only the devices the adapters heard"}
	}
	return verdict{ConditionTrue, conditionScanned, "Every adapter scanned the bus"}
}

// mergeDevices merges the devices of every current adapter into one
// list for the wire. It visits adapters in the order of spec.adapters,
// so when two adapters report different values of the same field, the
// earlier adapter's value wins. It leaves out the devices of a stale
// or stopped adapter, because they describe the wire as it was. Two
// reports are the same device when their physical addresses match. An
// adapter that heard a device but has not received its physical
// address reports it with none, and that report matches a device at
// the same logical address.
func mergeDevices(spec CECBusSpec, entries reports) []CECDevice {
	var merged []CECDevice
	for _, adapter := range spec.Adapters {
		for _, device := range entries.current[adapter.Machine].Devices {
			index := slices.IndexFunc(merged, func(known CECDevice) bool {
				if device.PhysicalAddress != "" && known.PhysicalAddress == device.PhysicalAddress {
					return true
				}
				return known.LogicalAddress == device.LogicalAddress && (device.PhysicalAddress == "" || known.PhysicalAddress == "")
			})
			if index < 0 {
				merged = append(merged, device)
				continue
			}
			merged[index] = fillDevice(merged[index], device)
		}
	}
	// Sorting physical addresses as text puts the TV first and each
	// device after the device it is connected to. Devices with no
	// physical address come last, in logical address order.
	slices.SortStableFunc(merged, func(a, b CECDevice) int {
		switch {
		case a.PhysicalAddress == "" && b.PhysicalAddress != "":
			return 1
		case b.PhysicalAddress == "" && a.PhysicalAddress != "":
			return -1
		}
		if order := strings.Compare(a.PhysicalAddress, b.PhysicalAddress); order != 0 {
			return order
		}
		return a.LogicalAddress - b.LogicalAddress
	})
	return merged
}

// fillDevice returns first with each empty field filled from second.
// The System Audio Mode is the exception. It has a timestamp, so the
// report with the later SystemAudioModeAt wins, because an adapter that
// missed the receiver's last broadcast still has the previous mode.
func fillDevice(first, second CECDevice) CECDevice {
	if second.SystemAudioMode != "" && second.SystemAudioModeAt > first.SystemAudioModeAt {
		first.SystemAudioMode, first.SystemAudioModeAt = second.SystemAudioMode, second.SystemAudioModeAt
	}
	fill := func(field *string, other string) {
		if *field == "" {
			*field = other
		}
	}
	fill(&first.PhysicalAddress, second.PhysicalAddress)
	fill(&first.Type, second.Type)
	fill(&first.OSDName, second.OSDName)
	fill(&first.Vendor, second.Vendor)
	fill(&first.CECVersion, second.CECVersion)
	fill(&first.Power, second.Power)
	return first
}
