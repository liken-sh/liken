package main

// The Deployment derives the status of each Television from its CECBus,
// the Displays, and the Receivers, and chooses which Televisions
// discovery creates and deletes. The Deployment does this and not a node
// workload, because each node workload reports only its own adapters.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/equipment-operator/cec"
)

// televisionDerived is the status the Deployment writes for one
// Television.
type televisionDerived struct {
	cec           *TelevisionCECStatus
	power         string
	activeSource  string
	activeDisplay string
	displays      []TelevisionDisplay
	reachable     Condition
	inCharge      Condition
}

// deriveTelevision returns the derived status of one Television.
// inCharge is the name of the Television that televisionFor chose for
// the bus. bus carries the device list and conditions that the
// Deployment derived in the same pass, and is nil when the CECBus does
// not exist.
func deriveTelevision(television *Television, inCharge string, bus *CECBus, displays []Display, receivers []Receiver, live podLiveness, now time.Time) televisionDerived {
	derived := televisionDerived{}
	name := television.bus()
	tv := tvOf(bus)
	if tv != nil {
		derived.cec = &TelevisionCECStatus{
			PhysicalAddress: tv.PhysicalAddress,
			LogicalAddress:  tv.LogicalAddress,
			OSDName:         tv.OSDName,
			Vendor:          tv.Vendor,
			CECVersion:      tv.CECVersion,
		}
		derived.power = tv.Power
	}
	if bus != nil {
		derived.displays = televisionDisplays(bus, displays, receivers)
		derived.activeSource = activeSourceOf(bus, live)
		derived.activeDisplay = activeDisplayOf(bus, displays, derived.activeSource)
	}
	verdict := televisionReachable(name, bus, tv)
	derived.reachable = stampCondition(conditionReachable, verdict, television.Metadata.Generation, television.Status.Conditions, now)
	charge := televisionInCharge(television.Metadata.Name, inCharge, name)
	derived.inCharge = stampCondition(conditionInCharge, charge, television.Metadata.Generation, television.Status.Conditions, now)
	return derived
}

// televisionInCharge returns the InCharge condition. When several
// Televisions name one bus, the operator applies spec.power only from
// the one named by inCharge. When that one is deleted, the next one
// that televisionFor chooses is treated as a new Television, and the node
// workload applies its spec.power once.
func televisionInCharge(name, inCharge, bus string) verdict {
	if inCharge == name {
		return verdict{ConditionTrue, reasonInCharge, fmt.Sprintf("This Television represents the TV on CECBus %s", bus)}
	}
	return verdict{ConditionFalse, reasonAnotherInCharge, fmt.Sprintf(
		"Television %s represents the TV on CECBus %s, so the operator does not apply the spec.power of this Television. When %s is deleted, this Television represents the TV, and the operator applies its spec.power once",
		inCharge, bus, inCharge)}
}

// tvOf returns the device at the TV's logical address, 0, in a bus's
// device list, or nil.
func tvOf(bus *CECBus) *CECDevice {
	if bus == nil {
		return nil
	}
	for index := range bus.Status.Devices {
		if bus.Status.Devices[index].LogicalAddress == int(cec.AddressTV) {
			return &bus.Status.Devices[index]
		}
	}
	return nil
}

// televisionReachable returns the Reachable condition, which is true
// when the TV answers Give Device Power Status. A TV that acknowledges
// logical address 0 and does not answer is not reachable, because a TV
// in deep standby can stop answering CEC, and then the operator cannot
// wake it.
func televisionReachable(name string, bus *CECBus, tv *CECDevice) verdict {
	switch {
	case bus == nil:
		return verdict{ConditionFalse, reasonNoBus, fmt.Sprintf("CECBus %s does not exist", name)}
	case bus.Spec.Mode != CECControl:
		return verdict{ConditionUnknown, reasonListening, fmt.Sprintf("CECBus %s is in Listen, so no adapter reads the power of the TV", name)}
	case tv == nil:
		scanned := conditionOf(bus.Status.Conditions, conditionScanned)
		if scanned.Reason == reasonStale || scanned.Reason == reasonStopped {
			return verdict{scanned.Status, scanned.Reason, fmt.Sprintf("CECBus %s: %s", name, scanned.Message)}
		}
		if scanned.Status == ConditionTrue {
			return verdict{ConditionFalse, reasonNotFound, fmt.Sprintf("No adapter of CECBus %s finds a TV at logical address 0", name)}
		}
		return verdict{ConditionUnknown, reasonNotScanned, fmt.Sprintf("CECBus %s has not finished a scan: %s", name, scanned.Message)}
	case tv.Power == "":
		return verdict{ConditionFalse, reasonNoPower, fmt.Sprintf(
			"The TV acknowledges logical address 0 on CECBus %s and does not reply to Give Device Power Status. A TV in a deep standby or an eco mode can stop replying, and the operator cannot wake it then", name)}
	}
	return verdict{ConditionTrue, reasonAnswers, fmt.Sprintf("The TV replies to Give Device Power Status on CECBus %s", name)}
}

// activeSourceOf returns the physical address of the last Active Source
// on a bus, or an empty string. It reads the first adapter in
// spec.adapters whose current report has an Active Source. Every
// adapter on the bus receives the same broadcasts and records its own
// Active Source too, so any adapter gives the same answer. Taking the
// first in spec order keeps the result stable from pass to pass.
func activeSourceOf(bus *CECBus, live podLiveness) string {
	entries := reportedEntries(bus, live)
	for _, adapter := range bus.Spec.Adapters {
		if entry, current := entries.current[adapter.Machine]; current && entry.ActiveSource != "" {
			return entry.ActiveSource
		}
	}
	return ""
}

// activeDisplayOf returns the name of the Display at the active source's
// physical address, or an empty string, as when the source is a
// streaming box. Several Displays can have the address, because a
// physical address is unique only within one bus. A Display that an
// adapter of the bus names comes first, because that adapter announces
// the Display's address on this bus. Next comes a Display on a machine
// that has an adapter of the bus. Within each group, the first by name
// wins.
func activeDisplayOf(bus *CECBus, displays []Display, source string) string {
	address, err := cec.ParsePhysicalAddress(source)
	if err != nil {
		return ""
	}
	var named, onMachine []string
	for _, display := range displays {
		at, err := cec.ParsePhysicalAddress(display.Status.PhysicalAddress)
		if err != nil || at != address {
			continue
		}
		switch {
		case slices.ContainsFunc(bus.Spec.Adapters, func(adapter CECBusAdapter) bool { return adapter.Display == display.Metadata.Name }):
			named = append(named, display.Metadata.Name)
		case slices.ContainsFunc(bus.Spec.Adapters, func(adapter CECBusAdapter) bool { return adapter.Machine == display.Status.Node }):
			onMachine = append(onMachine, display.Metadata.Name)
		}
	}
	for _, found := range [][]string{named, onMachine} {
		if len(found) > 0 {
			return slices.Min(found)
		}
	}
	return ""
}

// conditionOf returns the condition of the given type, or an empty
// Condition.
func conditionOf(conditions []Condition, kind string) Condition {
	for _, condition := range conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return Condition{}
}

// televisionDisplays lists the Displays that the bus's adapters name and
// that have a physical address, sorted by physical address. An adapter
// announces its Display's address on the bus, so that Display's picture
// reaches this TV. Another output of the same machine can go to another
// TV, and the same address can exist on another bus, so a Display that
// no adapter names is left out. Otherwise a session could match it and
// wake the wrong TV. In Listen mode no adapter announces an address, so
// the list is empty.
func televisionDisplays(bus *CECBus, displays []Display, receivers []Receiver) []TelevisionDisplay {
	if bus.Spec.Mode != CECControl {
		return nil
	}
	var listed []TelevisionDisplay
	for _, display := range displays {
		named := slices.ContainsFunc(bus.Spec.Adapters, func(adapter CECBusAdapter) bool {
			return adapter.Display == display.Metadata.Name
		})
		if !named {
			continue
		}
		if _, err := cec.ParsePhysicalAddress(display.Status.PhysicalAddress); err != nil {
			continue
		}
		listed = append(listed, TelevisionDisplay{
			Name:            display.Metadata.Name,
			PhysicalAddress: display.Status.PhysicalAddress,
			Via:             receiverFeeding(receivers, display, bus),
		})
	}
	slices.SortFunc(listed, func(a, b TelevisionDisplay) int {
		if order := strings.Compare(a.PhysicalAddress, b.PhysicalAddress); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	return listed
}

// receiverFeeding returns the Receiver that a Display's picture passes
// through, or nil. That is the first Receiver by name with an input whose
// machine and monitor are the Display's node and name, and only when the
// bus has an audio system above the Display's physical address. The
// input must match the machine as well as the monitor, because a
// receiver sends the same EDID on every input, so two machines on one
// receiver can read the same monitor. The audio system must be on the
// path, because an input such as an optical one can carry the sound of a
// Display that is connected straight to the TV.
func receiverFeeding(receivers []Receiver, display Display, bus *CECBus) *EquipmentRef {
	address, _ := cec.ParsePhysicalAddress(display.Status.PhysicalAddress)
	onPath := slices.ContainsFunc(bus.Status.Devices, func(device CECDevice) bool {
		above, err := cec.ParsePhysicalAddress(device.PhysicalAddress)
		return err == nil && device.Type == string(cec.TypeAudioSystem) && above.Above(address)
	})
	if !onPath {
		return nil
	}
	var found []string
	for _, receiver := range receivers {
		for _, input := range receiver.Spec.Inputs {
			if input.Machine == display.Status.Node && input.Monitor == display.Metadata.Name {
				found = append(found, receiver.Metadata.Name)
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &EquipmentRef{Kind: "Receiver", Name: slices.Min(found)}
}

// discoverTelevisions returns the buses that need a discovered
// Television, and the names of the discovered Televisions to delete. A
// bus in Control mode with a TV in its device list needs one when no
// Television names the bus and no other Television already has the
// bus's name. A discovered Television is deleted when televisionFor
// chooses a declared Television for its bus instead. A person who
// applies their own spec to a discovered Television keeps it, because it
// is still the only Television for the bus.
func discoverTelevisions(buses []CECBus, televisions []Television) (create, prune []string) {
	taken := map[string]bool{}
	for index := range televisions {
		television := &televisions[index]
		taken[television.Metadata.Name] = true
		if television.discovered() && televisionFor(televisions, television.bus()) != television {
			prune = append(prune, television.Metadata.Name)
		}
	}
	for index := range buses {
		bus := &buses[index]
		if bus.Spec.Mode != CECControl || tvOf(bus) == nil || televisionFor(televisions, bus.Metadata.Name) != nil {
			continue
		}
		if taken[discoveredTelevisionName(bus.Metadata.Name)] {
			continue
		}
		create = append(create, bus.Metadata.Name)
	}
	return create, prune
}

// discoveredTelevisionName returns the name of the Television that
// discovery creates for a bus, which is the bus's name.
func discoveredTelevisionName(bus string) string {
	return bus
}
