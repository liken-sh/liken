package main

// What the Deployment derives for one CECBus from the reports of its
// adapters: one device list for the whole wire, and the conditions
// that say whether the adapters did what the mode asks. Each node
// workload reports only what its own adapter sees, so only a component
// that reads every report can merge them.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The CECBus conditions and their reasons.
const (
	conditionAddressKnown = "AddressKnown"
	conditionJoined       = "Joined"
	conditionCoherent     = "Coherent"
	conditionScanned      = "Scanned"

	reasonListening   = "Listening"
	reasonNotReported = "NotReported"
	reasonRefused     = "Refused"
	reasonNoAddress   = "NoAddress"
	reasonNoLogical   = "NoLogicalAddress"
	reasonOneAdapter  = "OneAdapter"
	reasonApart       = "Apart"
	reasonScanning    = "Scanning"
	reasonNoAnswer    = "NoAnswer"
	reasonSilent      = "Silent"
	reasonStale       = "Stale"
	reasonStopped     = "Stopped"
)

// staleAfter is how old an entry's report may be before the verdicts
// stop trusting it. The node workload writes its entry every
// cecReportInterval, and a pod that dies writes nothing more, so an
// entry older than three intervals belongs to a pod that is gone. The
// Deployment's backstop tick derives every bus again, because a pod
// that dies sends no event.
var staleAfter = 3 * cecReportInterval

// verdict is one condition before it carries its generation and its
// transition time.
type verdict struct {
	status  ConditionStatus
	reason  string
	message string
}

// deriveCECBus answers the device list and the conditions of one bus.
// Only the entries of machines the spec names count, because an entry
// of a machine the spec dropped describes another wire.
func deriveCECBus(bus *CECBus, now time.Time) ([]CECDevice, []Condition) {
	entries := reportedEntries(bus, now)
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

// reports are the entries of the machines the spec names, split into
// the entries that describe the adapter now and the verdicts for the
// ones that do not: a stale entry and a stopped adapter.
type reports struct {
	current map[string]CECAdapterStatus
	gone    map[string]verdict
}

// reportedEntries sorts each entry of a machine the spec names. A
// machine with no entry is in neither map.
func reportedEntries(bus *CECBus, now time.Time) reports {
	sorted := reports{current: map[string]CECAdapterStatus{}, gone: map[string]verdict{}}
	for _, entry := range bus.Status.Adapters {
		if _, named := bus.Spec.names(entry.Machine); !named {
			continue
		}
		if absent, isGone := goneVerdict(entry, now); isGone {
			sorted.gone[entry.Machine] = absent
			continue
		}
		sorted.current[entry.Machine] = entry
	}
	return sorted
}

// goneVerdict answers the verdict for an entry that no longer describes
// the adapter, and false for an entry that does.
func goneVerdict(entry CECAdapterStatus, now time.Time) (verdict, bool) {
	if entry.State == AdapterStopped {
		return verdict{ConditionFalse, reasonStopped, fmt.Sprintf("the node workload on %s stopped and released the adapter: %s", entry.Machine, entry.Message)}, true
	}
	reported, err := time.Parse(time.RFC3339, entry.ReportedAt)
	if err != nil || now.Sub(reported) > staleAfter {
		return verdict{ConditionUnknown, reasonStale, fmt.Sprintf("the node workload on %s last reported at %s, more than %s ago; it may have stopped",
			entry.Machine, entry.ReportedAt, staleAfter)}, true
	}
	return verdict{}, false
}

// lookup finds one machine's entry, or the verdict that replaces it.
// A machine with neither answers notReported.
func (r reports) lookup(machine string, missing ConditionStatus) (CECAdapterStatus, *verdict) {
	if absent, isGone := r.gone[machine]; isGone {
		return CECAdapterStatus{}, &absent
	}
	if entry, held := r.current[machine]; held {
		return entry, nil
	}
	absent := notReported(machine, missing)
	return CECAdapterStatus{}, &absent
}

// stampCondition gives a verdict its generation and keeps the moment
// the verdict last changed, so the stamp moves only when the status
// flips.
func stampCondition(kind string, v verdict, generation int64, previous []Condition, now time.Time) Condition {
	condition := Condition{
		Type:               kind,
		Status:             v.status,
		ObservedGeneration: generation,
		Reason:             v.reason,
		Message:            v.message,
		LastTransitionTime: timestamp(now),
	}
	for _, held := range previous {
		if held.Type == kind && held.Status == v.status && held.LastTransitionTime != "" {
			condition.LastTransitionTime = held.LastTransitionTime
		}
	}
	return condition
}

// notReported is the verdict for a machine whose node workload wrote
// no entry: the machine has no adapter, or the pod does not run there.
func notReported(machine string, status ConditionStatus) verdict {
	return verdict{status, reasonNotReported, fmt.Sprintf(
		"no equipment-operator-cec pod reports an adapter on machine %s: check that the machine publishes a -cec device and that the pod runs there", machine)}
}

// refused is the verdict for an adapter whose node workload reports a
// failed call, in that call's own words.
func refused(entry CECAdapterStatus) verdict {
	return verdict{ConditionFalse, reasonRefused, fmt.Sprintf("the adapter on %s: %s", entry.Machine, entry.Message)}
}

var listenOnly = verdict{ConditionFalse, reasonListening, "the bus is in Listen, so no adapter claims a logical address or announces a physical address"}

// addressKnown: every adapter in Control has a physical address from
// its Display.
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
			return verdict{ConditionFalse, reasonNoAddress, fmt.Sprintf("the adapter on %s has no physical address: %s", adapter.Machine, entry.Message)}
		case entry.PhysicalAddress == "":
			return verdict{ConditionFalse, reasonNoAddress, fmt.Sprintf("the adapter on %s has no physical address from Display %s", adapter.Machine, adapter.Display)}
		}
	}
	return verdict{ConditionTrue, conditionAddressKnown, "every adapter announces its Display's physical address"}
}

// joined: every adapter in Control holds a logical address.
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
			return verdict{ConditionFalse, reasonNoLogical, fmt.Sprintf("the adapter on %s holds no logical address yet", adapter.Machine)}
		}
	}
	return verdict{ConditionTrue, conditionJoined, "every adapter holds a logical address"}
}

// coherent: each adapter that finished a scan sees every other one by
// its OSD name. An adapter that never sees the others is on a different
// wire than the spec states. The logical address is not compared,
// because an adapter that joins again can take another one, and the
// other adapter's last scan still names the old one.
func coherent(spec CECBusSpec, entries reports) verdict {
	if spec.Mode != CECControl {
		return verdict{ConditionUnknown, reasonListening, "the bus is in Listen, and an adapter in Listen announces no OSD name for the others to see"}
	}
	var members []CECAdapterStatus
	for _, adapter := range spec.Adapters {
		if absent, isGone := entries.gone[adapter.Machine]; isGone {
			return absent
		}
		if entry, held := entries.current[adapter.Machine]; held && entry.State == AdapterScanned {
			members = append(members, entry)
		}
	}
	if len(members) < 2 {
		return verdict{ConditionTrue, reasonOneAdapter, "at most one adapter has scanned the bus, so no adapter has another to see"}
	}
	for _, seer := range members {
		for _, seen := range members {
			if seer.Machine != seen.Machine && !sees(seer, seen) {
				return verdict{ConditionFalse, reasonApart, fmt.Sprintf(
					"the adapter on %s does not see the adapter on %s by its OSD name %q; the two adapters may be on different wires",
					seer.Machine, seen.Machine, seen.OSDName)}
			}
		}
	}
	return verdict{ConditionTrue, conditionCoherent, "every adapter sees every other adapter on the bus"}
}

// sees answers whether one adapter's devices include another adapter.
func sees(seer, seen CECAdapterStatus) bool {
	for _, device := range seer.Devices {
		if device.OSDName == seen.OSDName {
			return true
		}
	}
	return false
}

// silentCable is the end of the message when an adapter finds no
// device. A drill found that many HDMI cables leave out the CEC wire,
// and an adapter on such a cable hears nothing and every poll it sends
// goes unacknowledged. The message names no device at the cable's far
// end, because an adapter can connect to a receiver's input or sit
// inline between a machine and a TV.
const silentCable = "the HDMI cable at the adapter's output may not carry the CEC wire"

// scanned: the device list is complete. In Control that means every
// adapter finished a scan and found at least one device. In Listen the
// list is never complete, because a full list needs polls, and a poll
// is a transmission.
func scanned(spec CECBusSpec, entries reports) verdict {
	for _, adapter := range spec.Adapters {
		entry, absent := entries.lookup(adapter.Machine, ConditionUnknown)
		switch {
		case absent != nil:
			return *absent
		case entry.State == AdapterRefused:
			return refused(entry)
		case spec.Mode == CECListen && len(entry.Devices) == 0:
			return verdict{ConditionFalse, reasonSilent, fmt.Sprintf("the adapter on %s has heard no device yet; %s", adapter.Machine, silentCable)}
		case spec.Mode == CECControl && entry.State != AdapterScanned:
			return verdict{ConditionUnknown, reasonScanning, fmt.Sprintf("the adapter on %s has not finished a scan", adapter.Machine)}
		case spec.Mode == CECControl && len(entry.Devices) == 0:
			return verdict{ConditionFalse, reasonNoAnswer, fmt.Sprintf("no device answered the polls of the adapter on %s; %s", adapter.Machine, silentCable)}
		}
	}
	if spec.Mode != CECControl {
		return verdict{ConditionFalse, reasonListening, "the bus is in Listen, so the device list holds only the devices the adapters heard"}
	}
	return verdict{ConditionTrue, conditionScanned, "every adapter scanned the bus"}
}

// mergeDevices folds every current adapter's devices into one list for
// the wire, in the order the spec names the adapters, so the first
// adapter states a fact another adapter also states. A stale or stopped
// adapter's devices are left out, because they describe the wire as it
// was. Two reports are one device when their physical addresses
// match. A report with no physical address, which a monitor makes
// before it hears one, is the device at the same logical address.
func mergeDevices(spec CECBusSpec, entries reports) []CECDevice {
	var merged []CECDevice
	for _, adapter := range spec.Adapters {
		for _, device := range entries.current[adapter.Machine].Devices {
			index := slices.IndexFunc(merged, func(held CECDevice) bool {
				if device.PhysicalAddress != "" && held.PhysicalAddress == device.PhysicalAddress {
					return true
				}
				return held.LogicalAddress == device.LogicalAddress && (device.PhysicalAddress == "" || held.PhysicalAddress == "")
			})
			if index < 0 {
				merged = append(merged, device)
				continue
			}
			merged[index] = fillDevice(merged[index], device)
		}
	}
	// The TV comes first and each device follows the device it is
	// connected to, because a physical address sorts that way when it
	// is read as text. A device with no physical address comes last.
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

// fillDevice keeps each fact the first report states and takes the
// facts it lacks from the second.
func fillDevice(first, second CECDevice) CECDevice {
	fill := func(held *string, other string) {
		if *held == "" {
			*held = other
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
