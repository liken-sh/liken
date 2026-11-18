package main

// The Deployment's pass over Televisions. It runs at the end of each
// CECBus pass, because a Television's status is derived from the device
// list and conditions that the CECBus pass computes. It creates a
// Television for each bus in Control mode whose TV no Television names,
// deletes a discovered Television when another Television names the
// same bus, and writes each Television's status when it changed.

import (
	"fmt"
	"os"
	"reflect"
	"slices"
)

// passTelevisions runs the Television pass on the buses that the CECBus
// pass derived. live reports which of the node workload's pods are
// running. When a read fails, the pass stops and runs again after
// cecBusRetry, because a status derived from a partial read would
// delete facts that are still true. The reads replace a cached copy
// that is older than this process's last write with the API server's
// (objectcache.go), so the pass does not create a Television or write
// a status twice.
func (c *cecBusController) passTelevisions(buses []CECBus, live podLiveness) {
	televisions, err := readTelevisions(c.client, c.televisions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions: %v\n", err)
		c.retryLater()
		return
	}
	displays, err := readDisplays(c.client, c.displays)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Displays: %v\n", err)
		c.retryLater()
		return
	}
	receivers, err := readReceivers(c.client, c.receivers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Receivers: %v\n", err)
		c.retryLater()
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
			c.retryLater()
			continue
		}
		why := replacedBy(televisions.Items, name)
		fmt.Fprintf(c.log, "deleted the discovered Television %s: %s\n", name, why)
		if bus, found := byName[name]; found {
			c.recorder.Normal(reference("CECBus", bus.Metadata), reasonTelevisionDeleted, fmt.Sprintf("deleted the discovered Television %s: %s", name, why))
		}
	}
	derivedOf := map[string]televisionDerived{}
	for index := range televisions.Items {
		television := &televisions.Items[index]
		if slices.Contains(prune, television.Metadata.Name) {
			continue
		}
		inCharge := ""
		if chosen := televisionFor(televisions.Items, television.bus()); chosen != nil {
			inCharge = chosen.Metadata.Name
		}
		derived := deriveTelevision(television, inCharge, byName[television.bus()], displays.Items, receivers.Items, live, c.now())
		derivedOf[television.Metadata.Name] = derived
		if televisionUnchanged(television.Status, derived) {
			continue
		}
		if err := ApplyTelevisionDerived(c.client, television.Metadata.Name, derived); err != nil {
			fmt.Fprintf(os.Stderr, "writing the status of Television %s: %v\n", television.Metadata.Name, err)
			c.retryLater()
			continue
		}
		postTransitions(c.recorder, reference("Television", television.Metadata), television.Status.Conditions, []Condition{derived.reachable, derived.inCharge})
	}
	c.passReceiverCEC(receivers.Items, receiverBuses(televisions.Items, derivedOf), byName)
}

// createDiscoveredTelevision creates the discovered Television for bus,
// and logs it and posts an Event only when this call created it.
func (c *cecBusController) createDiscoveredTelevision(bus string, found *CECBus) {
	created, err := CreateDiscoveredTelevision(c.client, bus)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating Television %s: %v\n", discoveredTelevisionName(bus), err)
		c.retryLater()
		return
	}
	if !created {
		return
	}
	message := fmt.Sprintf("CECBus %s reports a TV at %s named %q, and no Television names the bus, so the operator created Television %s",
		bus, tvOf(found).PhysicalAddress, tvOf(found).OSDName, discoveredTelevisionName(bus))
	fmt.Fprintln(c.log, message)
	c.recorder.Normal(reference("CECBus", found.Metadata), reasonTelevisionCreated, message)
}

// replacedBy names the Television that replaces the discovered
// Television name, for the log line and the Event. Neither lookup can
// miss, because discoverTelevisions returns a name to delete only when
// another Television in this list is in charge of its bus.
func replacedBy(televisions []Television, name string) string {
	index := slices.IndexFunc(televisions, func(television Television) bool { return television.Metadata.Name == name })
	bus := televisions[index].bus()
	return fmt.Sprintf("Television %s names CECBus %s", televisionFor(televisions, bus).Metadata.Name, bus)
}

// televisionUnchanged reports whether the stored status equals the
// derived one. It compares only the fields and conditions that the
// Deployment writes, because the node workloads write the others.
func televisionUnchanged(status TelevisionStatus, derived televisionDerived) bool {
	return reflect.DeepEqual(status.CEC, derived.cec) &&
		status.Power == derived.power &&
		status.ActiveSource == derived.activeSource &&
		status.ActiveDisplay == derived.activeDisplay &&
		reflect.DeepEqual(status.Displays, derived.displays) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionReachable), derived.reachable) &&
		reflect.DeepEqual(conditionOf(status.Conditions, conditionInCharge), derived.inCharge)
}
