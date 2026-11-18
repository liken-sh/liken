package main

// The pass of the node workload: choose the CECBus for this machine's
// adapter, create one when none names the machine, and take the adapter
// off a bus it leaves.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// pass finds this machine's bus and applies the bus's spec to the
// adapter. It returns only an adapter error. It logs an API error and
// requests another pass.
func (n *cecNode) pass(ctx context.Context) error {
	// The watch's store can lag behind this workload's own writes, and a
	// stale read would make the pass create a CECBus that exists, or
	// apply a spec.power again. So the reads return the API server's copy
	// of each object this workload wrote (objectcache.go).
	list, err := readCECBuses(n.client, n.buses)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing CECBuses: %v\n", err)
		n.retryLater()
		return nil
	}
	bus := n.choose(list)
	n.mutex.Lock()
	previous := n.bus
	n.mutex.Unlock()
	chosen := ""
	if bus != nil {
		chosen = bus.Metadata.Name
	}
	if chosen != previous {
		fmt.Fprintf(n.log, "the adapter on %s moves from %s to %s\n", n.machine, busLogName(previous), busLogName(chosen))
	}
	if previous != "" && chosen != previous {
		n.leave(previous)
	}
	if bus == nil {
		return n.idle()
	}
	recheck := reportedEntries(bus, n.liveness()).recheck
	n.mutex.Lock()
	n.bus = bus.Metadata.Name
	n.stored = storedEntry(bus, n.machine)
	n.recheck = recheck
	n.mutex.Unlock()
	adapter, _ := bus.Spec.names(n.machine)
	want := n.desired(bus.Spec, adapter.Display)
	if err := n.apply(ctx, want); err != nil {
		return err
	}
	if err := n.syncAddresses(ctx, want); err != nil {
		return err
	}
	n.passTelevisions(bus)
	return nil
}

// passTelevisions reads the bus's Television once and runs each
// handler on it. When the list fails, it only retries the result writes
// that the API server has not accepted. It cancels no running wake,
// standby, or power command, because the Television may still ask for
// it.
func (n *cecNode) passTelevisions(bus *CECBus) {
	list, err := readTelevisions(n.client, n.televisions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Televisions: %v\n", err)
		n.retryLater()
		n.writePower()
		n.writeWake()
		n.writeStandby()
		return
	}
	television := televisionFor(list.Items, bus.Metadata.Name)
	n.writeScreenAsk(television)
	// The power command waits for the power read, so the read runs
	// first.
	n.passPowerRead(bus, television)
	n.holdRoom(bus, television)
	// passWake and passStandby run before passTelevision, so a new
	// generation of spec.power cancels a wake or standby in progress
	// before the power command sends anything. No wake or standby command
	// may follow the first command of the power command.
	n.passWake(bus, television)
	n.passStandby(bus, television)
	n.passShow(bus, television)
	n.passTelevision(bus, television)
}

// choose returns the CECBus for this machine's adapter, or nil when
// there is none yet. A declared CECBus that names the machine wins over
// a discovered one, and choose deletes the discovered CECBus, as
// discovery.go does for a WiiM Receiver. When no CECBus names the
// machine, choose creates one named after the machine, in Listen mode,
// and returns nil. The watch event for the new CECBus starts the next
// pass.
func (n *cecNode) choose(list *CECBusList) *CECBus {
	var declared, discovered []*CECBus
	taken := map[string]bool{}
	for index := range list.Items {
		bus := &list.Items[index]
		taken[bus.Metadata.Name] = true
		if _, named := bus.Spec.names(n.machine); !named {
			continue
		}
		if bus.Metadata.Labels[discoveredLabel] != "" {
			discovered = append(discovered, bus)
		} else {
			declared = append(declared, bus)
		}
	}
	if len(declared) > 0 {
		for _, own := range discovered {
			if err := DeleteCECBus(n.client, own.Metadata.Name); err != nil {
				fmt.Fprintf(os.Stderr, "pruning CECBus %s: %v\n", own.Metadata.Name, err)
				n.retryLater()
				continue
			}
			fmt.Fprintf(n.log, "deleted the discovered CECBus %s: CECBus %s names machine %s\n", own.Metadata.Name, declared[0].Metadata.Name, n.machine)
		}
		n.logDeclared(declared)
		return declared[0]
	}
	n.logDeclared(nil)
	if len(discovered) > 0 {
		return discovered[0]
	}
	if taken[n.machine] {
		fmt.Fprintf(os.Stderr, "a CECBus named %s exists and does not name machine %s, so the adapter makes no CECBus of its own\n", n.machine, n.machine)
		return nil
	}
	// The store can lack a CECBus that a person created a moment ago.
	// The create then returns a conflict and does not overwrite it.
	err := CreateDiscoveredCECBus(n.client, n.machine, n.machine)
	if errors.Is(err, apiclient.ErrConflict) {
		fmt.Fprintf(os.Stderr, "a CECBus named %s exists that the list did not hold yet, so the adapter creates no CECBus of its own, and the next pass reads it\n", n.machine)
		n.retryLater()
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating CECBus %s: %v\n", n.machine, err)
		n.retryLater()
		return nil
	}
	fmt.Fprintf(n.log, "no CECBus names machine %s, so the adapter created CECBus %s in %s, which sends nothing on the wire\n", n.machine, n.machine, CECListen)
	return nil
}

// leave removes this machine's entry from the status of a bus that the
// adapter left. It does not reset the adapter: when another bus names
// the machine, pass applies that bus's spec next, and when none does,
// idle resets the adapter.
func (n *cecNode) leave(bus string) {
	if err := ApplyCECAdapterStatus(n.client, bus, n.machine, nil); err != nil && err != apiclient.ErrNotFound {
		fmt.Fprintf(os.Stderr, "removing machine %s from CECBus %s: %v\n", n.machine, bus, err)
		n.retryLater()
	}
	n.mutex.Lock()
	n.bus = ""
	n.stored = nil
	n.mutex.Unlock()
}

// idle takes the adapter off the bus while no CECBus names its
// machine, so the adapter does not reply to the TV before a CECBus
// allows it. It returns an error only when the adapter is gone.
func (n *cecNode) idle() error {
	n.mutex.Lock()
	active := n.applied.mode != ""
	n.mutex.Unlock()
	if !active {
		return nil
	}
	n.stopMode()
	if err := n.release(); err != nil {
		if cec.IsGone(err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "releasing the adapter on %s: %v\n", n.machine, err)
	}
	n.directory.Reset()
	n.mutex.Lock()
	n.source = cec.InvalidPhysicalAddress
	n.applied, n.entry, n.retryAt, n.retryWait = adapterConfig{}, CECAdapterStatus{}, n.now(), 0
	n.mutex.Unlock()
	return nil
}

func busLogName(name string) string {
	if name == "" {
		return "none"
	}
	return "CECBus " + name
}

// logDeclared logs which bus the adapter follows when more than one
// declared bus names the machine. The pass runs on every change to any
// bus, so it logs only when the set of those buses changes.
func (n *cecNode) logDeclared(declared []*CECBus) {
	names := make([]string, 0, len(declared))
	for _, bus := range declared {
		names = append(names, bus.Metadata.Name)
	}
	slices.Sort(names)
	set := strings.Join(names, ", ")
	if set == n.declared {
		return
	}
	n.declared = set
	if len(names) > 1 {
		fmt.Fprintf(n.log, "%d CECBuses name machine %s: %s, so the adapter follows %s, the first by name\n",
			len(names), n.machine, set, declared[0].Metadata.Name)
	}
}

func storedEntry(bus *CECBus, machine string) *CECAdapterStatus {
	for index := range bus.Status.Adapters {
		if bus.Status.Adapters[index].Machine == machine {
			entry := bus.Status.Adapters[index]
			return &entry
		}
	}
	return nil
}
