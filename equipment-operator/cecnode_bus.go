package main

// Which CECBus the node workload's adapter belongs to: the pass that
// finds it, the discovery that makes one, and what the adapter does
// when it leaves one.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/equipment-operator/cec"
	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// pass finds this machine's bus and brings the adapter to its spec.
// Only a failure of the adapter itself is returned; an API failure is
// logged, and the next pass tries again.
func (n *cecNode) pass(ctx context.Context) error {
	// The node workload creates and deletes CECBuses and writes status on
	// both kinds, and it reads what it wrote. A store that has not yet
	// received a write would make the pass create a CECBus again, or wait
	// for a spec.power it already applied, so the reads replace such a
	// copy with the API server's (objectcache.go).
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
		fmt.Fprintf(n.log, "the adapter on %s moves from %s to %s\n", n.machine, busWords(previous), busWords(chosen))
	}
	if previous != "" && chosen != previous {
		n.leave(previous)
	}
	if bus == nil {
		return n.idle()
	}
	n.mutex.Lock()
	n.bus = bus.Metadata.Name
	n.mutex.Unlock()
	adapter, _ := bus.Spec.names(n.machine)
	want := n.desired(bus.Metadata.Name, bus.Spec.Mode, adapter.Display)
	if err := n.apply(ctx, want); err != nil {
		return err
	}
	if err := n.syncAddresses(ctx, want); err != nil {
		return err
	}
	n.passTelevisions(bus)
	return nil
}

// passTelevisions reads the bus's Television once for each of the
// Television's intents: its session's power read, the room its session
// holds, its spec.power, its session's wake, its session's standby, and
// a home press's ask to show the session's Display.
// A list that fails leaves each for the next pass, and cancels nothing,
// because the Television may still ask for what runs.
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
	// A power press waits for its read, so the read goes first.
	n.passPowerRead(bus, television)
	n.holdRoom(bus, television)
	// The wake and standby passes go next: a new generation of
	// spec.power stops a wake or a standby in progress there, so no
	// session command follows the power pass's first command.
	n.passWake(bus, television)
	n.passStandby(bus, television)
	n.passShow(bus, television)
	n.passTelevision(bus, television)
}

// choose answers the CECBus this machine's adapter belongs to. A
// person's CECBus that names the machine wins, and the node workload
// deletes the CECBus it discovered for the machine, as discovery.go
// does for a WiiM Receiver. With no CECBus naming the machine, the
// node workload creates one in Listen, named after the machine, and
// the watch brings it to the next pass.
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
	// The list reads each bus this workload wrote from the API server
	// and the rest from the store, which can lack a bus a person created
	// a moment ago. The create leaves such a bus alone.
	err := CreateDiscoveredCECBus(n.client, n.machine, n.machine)
	if errors.Is(err, apiclient.ErrConflict) {
		fmt.Fprintf(os.Stderr, "a CECBus named %s exists that the list did not hold yet, so the adapter makes no CECBus of its own; the next pass reads it\n", n.machine)
		n.retryLater()
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating CECBus %s: %v\n", n.machine, err)
		n.retryLater()
		return nil
	}
	fmt.Fprintf(n.log, "no CECBus names machine %s; created CECBus %s in %s, which sends nothing on the wire\n", n.machine, n.machine, CECListen)
	return nil
}

// leave removes this machine's entry from a bus it no longer belongs
// to. The adapter keeps what it found, because a person who adopts the
// discovered bus names the same wire under another name.
func (n *cecNode) leave(bus string) {
	if err := ApplyCECAdapterStatus(n.client, bus, n.machine, nil); err != nil && err != apiclient.ErrNotFound {
		fmt.Fprintf(os.Stderr, "removing machine %s from CECBus %s: %v\n", n.machine, bus, err)
		n.retryLater()
	}
	n.mutex.Lock()
	n.bus = ""
	n.written = nil
	n.mutex.Unlock()
}

// idle takes the adapter off the bus while no CECBus names its
// machine: it stops the mode's work, clears the logical addresses, and
// leaves the follower or monitor mode. No person has allowed the
// adapter on any bus then, so it must not answer the TV. Only a
// failure that means the adapter left is returned.
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

// busWords names a bus in a log line, and an empty name as none.
func busWords(name string) string {
	if name == "" {
		return "none"
	}
	return "CECBus " + name
}

// logDeclared states that more than one of a person's buses name the
// machine, which a person has to settle, and which one the adapter
// follows. The pass runs on every change to any bus, so the line
// prints only when the set of those buses changes.
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
		fmt.Fprintf(n.log, "%d CECBuses name machine %s: %s; the adapter follows %s, the first by name\n",
			len(names), n.machine, set, declared[0].Metadata.Name)
	}
}
