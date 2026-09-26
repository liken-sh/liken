package main

// The Deployment's pass over Televisions. It runs at the end of each
// CECBus pass, because a Television's status comes from the device
// list and the conditions that pass derives. It creates the
// Televisions discovery owns, deletes the ones a person's Television
// replaced, and writes each Television's derived status when it
// changed.

import (
	"fmt"
	"os"
	"reflect"
	"slices"
)

// passTelevisions derives every Television from the buses as the CECBus
// pass derived them. A read that fails skips the pass, because a
// status derived from a partial read would remove facts that are still
// true, and the next pass reads again.
func (c *cecBusController) passTelevisions(buses []CECBus) {
	televisions, err := ListTelevisions(c.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions: %v\n", err)
		return
	}
	displays, err := ListDisplays(c.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Displays: %v\n", err)
		return
	}
	receivers, err := ListReceivers(c.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Receivers: %v\n", err)
		return
	}
	create, prune := discoverTelevisions(buses, televisions.Items)
	for _, bus := range create {
		if err := CreateDiscoveredTelevision(c.client, bus); err != nil {
			fmt.Fprintf(os.Stderr, "creating Television %s: %v\n", discoveredTelevisionName(bus), err)
		}
	}
	for _, name := range prune {
		if err := DeleteTelevision(c.client, name); err != nil {
			fmt.Fprintf(os.Stderr, "pruning Television %s: %v\n", name, err)
		}
	}
	byName := map[string]*CECBus{}
	for index := range buses {
		byName[buses[index].Metadata.Name] = &buses[index]
	}
	for index := range televisions.Items {
		television := &televisions.Items[index]
		if slices.Contains(prune, television.Metadata.Name) {
			continue
		}
		inCharge := ""
		if chosen := televisionFor(televisions.Items, television.bus()); chosen != nil {
			inCharge = chosen.Metadata.Name
		}
		derived := deriveTelevision(television, inCharge, byName[television.bus()], displays.Items, receivers.Items, c.now())
		if televisionUnchanged(television.Status, derived) {
			continue
		}
		if err := ApplyTelevisionDerived(c.client, television.Metadata.Name, derived); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of Television %s: %v\n", television.Metadata.Name, err)
		}
	}
}

// televisionUnchanged answers whether the status already holds what
// the Deployment derived. Only the Deployment's own fields count: the
// node workload's powerGeneration and PowerApplied are not its to
// write.
func televisionUnchanged(status TelevisionStatus, derived televisionDerived) bool {
	return reflect.DeepEqual(status.CEC, derived.cec) &&
		status.Power == derived.power &&
		reflect.DeepEqual(status.Displays, derived.displays) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionReachable), derived.reachable) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionInCharge), derived.inCharge)
}
