package main

// Reading the bluetooth-operator's Peripherals.
//
// The bluetooth-operator publishes one cluster-scoped Peripheral per
// bonded device, and writes on it the two facts this layer draws: the
// Connected condition, which is the record of the controller's link,
// and the charge the device reports. A Peripheral is named by the
// device's lowercase dashed address, which is the same name a claim's
// allocation result carries as the device for the bluetooth.liken.sh
// driver. So a Remote's standing claim names the Peripheral of the
// controller it holds, and the operator resolves the claim's allocation
// to reach it. This layer reads a Peripheral and never writes one.

import (
	"fmt"
	"os"
)

// The group the bluetooth-operator serves. A Peripheral is
// cluster-scoped, because a bonded device belongs to no namespace.
const peripheralAPIVersion = "bluetooth.liken.sh/v1alpha1"

// The DRA driver whose devices are bonded Bluetooth peripherals. An
// allocation result from this driver names its Peripheral by the device
// name it carries.
const bluetoothDriver = "bluetooth.liken.sh"

// The condition that carries the link, and the value that means the link
// is up.
const (
	peripheralConnected = "Connected"
	conditionTrue       = "True"
)

// A Peripheral carries only what this operator reads: the link and the
// charge the device reports.
type Peripheral struct {
	Metadata ObjectMeta       `json:"metadata"`
	Status   PeripheralStatus `json:"status"`
}

type PeripheralList struct {
	Metadata ListMeta     `json:"metadata"`
	Items    []Peripheral `json:"items"`
}

type PeripheralStatus struct {
	Battery    *PeripheralBattery    `json:"battery,omitempty"`
	Conditions []PeripheralCondition `json:"conditions,omitempty"`
}

// The charge the device reports. A device that reports no level carries
// no battery block at all.
type PeripheralBattery struct {
	Percentage int `json:"percentage,omitempty"`
}

// One condition on a Peripheral. The operator reads the type and the
// status alone.
type PeripheralCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// peripheralDesk holds what one pass read about the controllers: the
// Peripherals by name, and the Peripheral each Remote's standing claim
// allocated, by controller key. The reconcile pass reads it when it
// builds a Player's bus status. Only the pass goroutine touches it, so
// it carries no mutex, unlike the desks the bus thread writes. A
// Peripheral change wakes the loop through the watch, and the pass then
// reads the collection again.
type peripheralDesk struct {
	held  map[string]Peripheral
	named map[string]string
}

func newPeripheralDesk() *peripheralDesk {
	return &peripheralDesk{
		held:  map[string]Peripheral{},
		named: map[string]string{},
	}
}

// hold replaces what the desk holds with what this pass read. The maps go
// in whole, so a Peripheral the cluster no longer holds and a controller
// whose claim lost its allocation leave no entry behind, and the desk
// needs no separate shrink.
func (p *peripheralDesk) hold(peripherals []Peripheral, named map[string]string) {
	held := make(map[string]Peripheral, len(peripherals))
	for _, peripheral := range peripherals {
		held[peripheral.Metadata.Name] = peripheral
	}
	p.held = held
	if named == nil {
		named = map[string]string{}
	}
	p.named = named
}

// peripheralFor names the Peripheral one controller's standing claim
// allocated. It is empty for a controller whose claim carries no
// allocation, and for one whose device comes from another driver.
func (p *peripheralDesk) peripheralFor(key string) string {
	return p.named[key]
}

// connectedFor reports one Peripheral's link and whether the desk holds an
// answer. A Peripheral the cluster does not hold, and one that carries no
// Connected condition, is neither connected nor disconnected, so the
// status it appears in carries no connected key at all.
func (p *peripheralDesk) connectedFor(name string) (connected, held bool) {
	peripheral, standing := p.held[name]
	if !standing {
		return false, false
	}
	for _, condition := range peripheral.Status.Conditions {
		if condition.Type == peripheralConnected {
			return condition.Status == conditionTrue, true
		}
	}
	return false, false
}

// batteryFor is the charge one Peripheral reports. A device that reports
// no level answers nil, and the status it appears in carries no battery
// key.
func (p *peripheralDesk) batteryFor(name string) *int {
	peripheral, standing := p.held[name]
	if !standing || peripheral.Status.Battery == nil {
		return nil
	}
	percentage := peripheral.Status.Battery.Percentage
	return &percentage
}

// observePeripherals reads the cluster's Peripherals and resolves which
// one each Remote holds. It runs before the pass writes any Player
// status, because a unit's bus status carries its controllers' links.
// It reads each Remote's standing claim, which is the one read of that
// claim the pass makes, and returns those reads by controller key so
// the standing reconcile makes none of its own. A claim read that fails
// has no entry, and the reconcile then reads that one claim itself. A
// Peripherals list that fails leaves the desk holding what it had, so
// one failed read does not blank every controller on the idle screen.
func (o *operator) observePeripherals(remotes []Remote) map[string]claimRead {
	claims := make(map[string]claimRead, len(remotes))
	named := make(map[string]string, len(remotes))
	for index := range remotes {
		remote := &remotes[index]
		key := controllerKey(remote.Metadata.Namespace, remote.Metadata.Name)
		read, err := o.readClaim(remote.Metadata.Namespace,
			remoteClaimName(remote.Metadata.Name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the claim of remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
			continue
		}
		claims[key] = read
		if name, held := peripheralOf(read.claim); held {
			named[key] = name
		}
	}
	list, err := ListPeripherals(o.client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing peripherals: %v\n", err)
		return claims
	}
	o.peripherals.hold(list.Items, named)
	return claims
}

// peripheralOf names the Peripheral a standing claim's allocation
// holds. The allocation result for the bluetooth.liken.sh driver names
// the device, and that device name is the Peripheral's own name,
// because the bluetooth-operator names both from the device's address.
// A claim the scheduler has not allocated names none, and so does a
// controller some other driver publishes.
func peripheralOf(claim *ResourceClaim) (string, bool) {
	if claim == nil || claim.Status == nil || claim.Status.Allocation == nil {
		return "", false
	}
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver == bluetoothDriver {
			return result.Device, true
		}
	}
	return "", false
}
