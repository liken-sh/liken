package main

// What the Deployment derives for one Television: the TV's facts and
// power from its CECBus, the Reachable condition, and the Displays
// whose pictures reach the TV. It also decides which Televisions
// discovery creates and deletes. The node workloads report the bus,
// and only a component that reads every report and the Displays can
// join them.

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/equipment-operator/cec"
)

// televisionDerived is what the Deployment writes for one Television.
type televisionDerived struct {
	cec       *TelevisionCECStatus
	power     string
	displays  []TelevisionDisplay
	reachable Condition
	inCharge  Condition
}

// deriveTelevision answers one Television's derived status. inCharge
// names the Television that speaks for the bus's TV, which televisionFor
// chose. bus is the CECBus the Television names, with the device list
// and the conditions the Deployment derived for it in the same pass, or
// nil when no such bus exists.
func deriveTelevision(television *Television, inCharge string, bus *CECBus, displays []Display, receivers []Receiver, now time.Time) televisionDerived {
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
	}
	verdict := televisionReachable(name, bus, tv)
	derived.reachable = stampCondition(conditionReachable, verdict, television.Metadata.Generation, television.Status.Conditions, now)
	charge := televisionInCharge(television.Metadata.Name, inCharge, name)
	derived.inCharge = stampCondition(conditionInCharge, charge, television.Metadata.Generation, television.Status.Conditions, now)
	return derived
}

// televisionInCharge: this Television speaks for the bus's TV. A bus
// has one TV, so of two Televisions on one bus only one is in charge,
// and the node workload applies only that one's spec.power. The other
// takes over when the first is deleted, and its spec.power is then
// applied once, as a new object's is.
func televisionInCharge(name, inCharge, bus string) verdict {
	if inCharge == name {
		return verdict{ConditionTrue, reasonInCharge, fmt.Sprintf("this Television speaks for the TV on CECBus %s", bus)}
	}
	return verdict{ConditionFalse, reasonAnotherInCharge, fmt.Sprintf(
		"Television %s speaks for the TV on CECBus %s, so the operator applies no spec.power of this Television; when %s is deleted, this Television speaks for the TV, and the operator applies its spec.power once then",
		inCharge, bus, inCharge)}
}

// tvOf finds the TV in a bus's device list. The TV always holds
// logical address 0.
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

// televisionReachable: the TV answers its power status on its bus. A
// TV that acknowledges its address and does not answer is not
// reachable, because a TV in a deep standby can stop answering, and
// the operator cannot wake it then.
func televisionReachable(name string, bus *CECBus, tv *CECDevice) verdict {
	switch {
	case bus == nil:
		return verdict{ConditionFalse, reasonNoBus, fmt.Sprintf("CECBus %s does not exist", name)}
	case bus.Spec.Mode != CECControl:
		return verdict{ConditionUnknown, reasonListening, fmt.Sprintf("CECBus %s is in Listen, so no adapter asks the TV for its power", name)}
	case tv == nil:
		scanned := conditionOf(bus.Status.Conditions, conditionScanned)
		if scanned.Reason == reasonStale || scanned.Reason == reasonStopped {
			return verdict{scanned.Status, scanned.Reason, fmt.Sprintf("CECBus %s: %s", name, scanned.Message)}
		}
		if scanned.Status == ConditionTrue {
			return verdict{ConditionFalse, reasonNotFound, fmt.Sprintf("no adapter of CECBus %s finds a TV at logical address 0", name)}
		}
		return verdict{ConditionUnknown, reasonNotScanned, fmt.Sprintf("CECBus %s has not finished a scan: %s", name, scanned.Message)}
	case tv.Power == "":
		return verdict{ConditionFalse, reasonNoPower, fmt.Sprintf(
			"the TV acknowledges logical address 0 on CECBus %s and does not answer Give Device Power Status; a TV in a deep standby or an eco mode can stop answering, and the operator cannot wake it then", name)}
	}
	return verdict{ConditionTrue, reasonAnswers, fmt.Sprintf("the TV answers Give Device Power Status on CECBus %s", name)}
}

// conditionOf finds one condition by type, and an empty condition when
// the list holds none of that type.
func conditionOf(conditions []Condition, kind string) Condition {
	for _, condition := range conditions {
		if condition.Type == kind {
			return condition
		}
	}
	return Condition{}
}

// televisionDisplays lists the Display that each adapter of a bus in
// Control names, when that Display has a physical address. Each such
// adapter announces its own Display's physical address on this wire,
// so that Display's picture enters this tree. A machine can have
// another output that goes to another TV, and a physical address does
// not name its tree, so a Display no adapter names is not listed: the
// session match would read it and could wake the wrong TV. In Listen
// no adapter announces an address, so the list is empty. The list is
// in tree order, as the bus's device list is.
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

// receiverFeeding answers the Receiver a Display's picture passes
// through: the first Receiver by name with an input that names the
// Display's machine and the Display as its monitor, when the bus has an
// audio system above the Display in the tree. A receiver sends one EDID
// on every input, so two machines on one receiver can read the same
// monitor id, and the machine is what tells them apart. An input can
// also name a machine whose picture does not pass through the receiver,
// such as an optical input for the sound of a Display straight on the
// TV, so the tree must show the receiver on the path.
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

// discoverTelevisions answers the buses that need a discovered
// Television and the discovered Televisions to delete. A bus in
// Control whose devices include a TV needs one when no Television
// names the bus. Discovery owns only the labeled Television with its
// bus's name, and deletes it only when another Television names the
// same bus: a person adopts it by applying their own spec to it, and
// it stays the only Television for the bus. A name that another
// Television already holds is left alone, because that object names
// another bus. buses carry the device lists the Deployment derived in
// the same pass.
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

// discoveredTelevisionName is the name discovery gives a bus's TV: the
// bus's own name. The kind already says that the object is a TV, so
// the name carries no suffix, as a CECBus name carries none.
func discoveredTelevisionName(bus string) string {
	return bus
}
