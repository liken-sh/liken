package main

// bluetooth-operator publishes one cluster-scoped Peripheral for each
// bonded device. media-operator reads two facts from it: the Connected
// condition, which is True while the controller's link is up, and the
// battery block, which has the charge the device reports. A Peripheral
// is named by the device's address, lowercase and dash-separated, and
// the bluetooth.liken.sh driver uses the same string as the device name
// in a claim's allocation. So media-operator finds the Peripheral of a
// Remote's controller from the allocation of the Remote's ResourceClaim.
// It never writes a Peripheral.

import (
	"fmt"
	"os"
	"slices"
	"sync"
)

const peripheralAPIVersion = "bluetooth.liken.sh/v1alpha1"

const bluetoothDriver = "bluetooth.liken.sh"

const (
	peripheralConnected = "Connected"
	conditionTrue       = "True"
)

// Peripheral has only the fields that media-operator reads.
type Peripheral struct {
	Metadata ObjectMeta       `json:"metadata"`
	Status   PeripheralStatus `json:"status"`
}

type PeripheralStatus struct {
	Battery    *PeripheralBattery    `json:"battery,omitempty"`
	Conditions []PeripheralCondition `json:"conditions,omitempty"`
}

// A device that reports no charge has no battery block.
type PeripheralBattery struct {
	Percentage int `json:"percentage,omitempty"`
}

type PeripheralCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

// peripheralCache caches what the last pass read: the Peripherals by
// name, and the name of the Peripheral allocated to each Remote's claim,
// by controller key. A change to a Peripheral starts a new pass through
// the watch, and that pass replaces the cache.
//
// The pass writes the cache. The pass and the bus reader both read it to
// build a Player's bus status, because the bus reader publishes that
// status when a Play ends. So one mutex covers both maps.
type peripheralCache struct {
	mutex        sync.Mutex
	byName       map[string]Peripheral
	byController map[string]string
}

func newPeripheralCache() *peripheralCache {
	return &peripheralCache{
		byName:       map[string]Peripheral{},
		byController: map[string]string{},
	}
}

// replace replaces both maps whole, so a Peripheral that no longer exists
// and a controller whose claim lost its allocation leave no entry.
func (p *peripheralCache) replace(peripherals []Peripheral, named map[string]string) {
	held := make(map[string]Peripheral, len(peripherals))
	for _, peripheral := range peripherals {
		held[peripheral.Metadata.Name] = peripheral
	}
	if named == nil {
		named = map[string]string{}
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.byName = held
	p.byController = named
}

// peripheralFor returns an empty string when the controller's claim has
// no allocation, or when the allocated device comes from another driver.
func (p *peripheralCache) peripheralFor(key string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.byController[key]
}

// connectedFor returns known as false when the Peripheral does not
// exist or has no Connected condition. The Player's bus status then
// leaves out the connected key.
func (p *peripheralCache) connectedFor(name string) (connected, known bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	peripheral, found := p.byName[name]
	if !found {
		return false, false
	}
	for _, condition := range peripheral.Status.Conditions {
		if condition.Type == peripheralConnected {
			return condition.Status == conditionTrue, true
		}
	}
	return false, false
}

// batteryFor returns nil when the Peripheral does not exist or reports
// no charge. The Player's bus status then leaves out the battery key.
func (p *peripheralCache) batteryFor(name string) *int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	peripheral, found := p.byName[name]
	if !found || peripheral.Status.Battery == nil {
		return nil
	}
	percentage := peripheral.Status.Battery.Percentage
	return &percentage
}

// observePeripherals runs before the pass writes any Player status,
// because a Player's bus status includes the links of its controllers.
// It returns the claim of each Remote by controller key, so the
// reconcile of the Remote's claim does not read it again. A claim that
// fails to read has no entry, and that reconcile reads it itself. When
// the list of Peripherals fails, the cache keeps its previous contents,
// so one failed read does not clear the link and charge of every
// controller.
func (o *operator) observePeripherals(remotes []Remote) map[string]claimRead {
	claims := make(map[string]claimRead, len(remotes))
	named := make(map[string]string, len(remotes))
	for index := range remotes {
		remote := &remotes[index]
		key := controllerKey(remote.Metadata.Namespace, remote.Metadata.Name)
		read, err := o.readClaim(remote.Metadata.Namespace,
			remoteClaimName(remote.Metadata.Name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the ResourceClaim of Remote %s/%s: %v\n",
				remote.Metadata.Namespace, remote.Metadata.Name, err)
			continue
		}
		claims[key] = read
		if name, allocated := peripheralOf(read.claim); allocated {
			named[key] = name
		}
	}
	list, err := o.view.Peripherals()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing Peripherals, so the cache keeps the previous ones: %v\n", err)
		return claims
	}
	before := o.peripherals.links()
	o.peripherals.replace(list, named)
	o.logLinks(before, o.peripherals.links())
	return claims
}

// links returns, by controller key, whether the link is up for each
// controller whose Peripheral has a Connected condition.
func (p *peripheralCache) links() map[string]bool {
	p.mutex.Lock()
	named := make(map[string]string, len(p.byController))
	for key, name := range p.byController {
		named[key] = name
	}
	p.mutex.Unlock()
	links := make(map[string]bool, len(named))
	for key, name := range named {
		if connected, known := p.connectedFor(name); known {
			links[key] = connected
		}
	}
	return links
}

// logLinks writes one line for each controller whose link changed
// between two passes. A controller with no link state in the earlier
// pass gets no line, so the first pass after a restart does not log
// every controller.
func (o *operator) logLinks(before, after map[string]bool) {
	keys := make([]string, 0, len(after))
	for key := range after {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		was, known := before[key]
		if !known || was == after[key] {
			continue
		}
		state, condition := "disconnected", "False"
		if after[key] {
			state, condition = "connected", conditionTrue
		}
		logLine(o.log, "remote %s: controller %s, Peripheral %s reports %s %s",
			key, state, o.peripherals.peripheralFor(key), peripheralConnected, condition)
	}
}

// peripheralOf returns false when the scheduler has not allocated the
// claim, or when another driver publishes the controller.
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
