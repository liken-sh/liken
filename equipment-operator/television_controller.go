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
//
// This pass creates, deletes, and writes the status of Televisions, and
// compares each status with the one stored. The store can hold the copy
// from before the pass's last write until the write's own event
// arrives, and can lack a Television the pass created, so the read
// replaces such a copy with the API server's and reads the created
// Television, and a pass does not create a Television again or write a
// status again (objectcache.go). This loop writes no Display and no
// Receiver.
func (c *cecBusController) passTelevisions(buses []CECBus) {
	televisions, err := readTelevisions(c.client, c.televisions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions: %v\n", err)
		return
	}
	displays, err := readDisplays(c.client, c.displays)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Displays: %v\n", err)
		return
	}
	receivers, err := readReceivers(c.client, c.receivers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Receivers: %v\n", err)
		return
	}
	byName := map[string]*CECBus{}
	for index := range buses {
		byName[buses[index].Metadata.Name] = &buses[index]
	}
	create, prune := discoverTelevisions(buses, televisions.Items)
	for _, bus := range create {
		c.createDiscoveredTelevision(bus, byName[bus])
	}
	for _, name := range prune {
		if err := DeleteTelevision(c.client, name); err != nil {
			fmt.Fprintf(os.Stderr, "pruning Television %s: %v\n", name, err)
			continue
		}
		why := replacedBy(televisions.Items, name)
		fmt.Fprintf(c.log, "deleted the discovered Television %s: %s\n", name, why)
		if bus, found := byName[name]; found {
			c.recorder.Normal(reference("CECBus", bus.Metadata), reasonTelevisionDeleted, fmt.Sprintf("deleted the discovered Television %s: %s", name, why))
		}
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
			continue
		}
		postTransitions(c.recorder, reference("Television", television.Metadata), television.Status.Conditions, []Condition{derived.reachable, derived.inCharge})
	}
}

// createDiscoveredTelevision creates the Television discovery makes for
// a bus's TV, and logs the creation. A create that lands on a name
// another writer just took is the API server's conflict, not an error:
// CreateDiscoveredTelevision reports it as no creation, so this logs
// nothing for an object it did not make.
func (c *cecBusController) createDiscoveredTelevision(bus string, found *CECBus) {
	created, err := CreateDiscoveredTelevision(c.client, bus)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating Television %s: %v\n", discoveredTelevisionName(bus), err)
		return
	}
	if !created {
		return
	}
	message := fmt.Sprintf("CECBus %s reports a TV at %s named %q, and no Television names the bus; created Television %s",
		bus, tvOf(found).PhysicalAddress, tvOf(found).OSDName, discoveredTelevisionName(bus))
	fmt.Fprintln(c.log, message)
	c.recorder.Normal(reference("CECBus", found.Metadata), reasonTelevisionCreated, message)
}

// replacedBy says which Television took over the bus of a discovered
// Television that discovery deletes. discoverTelevisions deletes one
// only from this list and only when another Television is in charge of
// its bus, so both lookups find what they look for.
func replacedBy(televisions []Television, name string) string {
	index := slices.IndexFunc(televisions, func(television Television) bool { return television.Metadata.Name == name })
	bus := televisions[index].bus()
	return fmt.Sprintf("Television %s names CECBus %s", televisionFor(televisions, bus).Metadata.Name, bus)
}

// televisionUnchanged answers whether the status already holds what
// the Deployment derived. Only the Deployment's own fields count: the
// node workloads' fields and conditions are not its to write.
func televisionUnchanged(status TelevisionStatus, derived televisionDerived) bool {
	return reflect.DeepEqual(status.CEC, derived.cec) &&
		status.Power == derived.power &&
		status.ActiveSource == derived.activeSource &&
		status.ActiveDisplay == derived.activeDisplay &&
		reflect.DeepEqual(status.Displays, derived.displays) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionReachable), derived.reachable) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionInCharge), derived.inCharge)
}
