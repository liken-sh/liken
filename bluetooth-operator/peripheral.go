package main

// The Peripheral object: the durable fact that one device holds a bond
// with one adapter.
//
// Adoption runs on every pass, in both directions. A bond in bluetoothd
// with no Peripheral gets one created, owned by the Adapter. A
// Peripheral whose bond is gone from bluetoothd keeps its object and
// reports the gap in status, because deleting a Peripheral means
// unpair, and that is a person's decision and not an operator's.
//
// The same code handles the migration. A machine that paired its
// controllers before this API existed holds bonds in bluetoothd and no
// objects at all, and the first pass of the new operator creates a
// Peripheral for each one and a Secret under each Peripheral. There is
// no separate migration path to keep working.

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// reconcilePeripherals makes the Peripherals under one Adapter agree
// with the bonds bluetoothd holds. claimed names the controllers a
// prepared claim holds right now, by the Peripheral resource name
// each one is published under.
func (i *inventory) reconcilePeripherals(adapter *Adapter, snapshot radioSnapshot, batteries map[bonds.Address]*hidBattery, claimed map[string]bool, pass *inventoryPass) {
	adapterKey := adapter.Metadata.Name
	peripherals, err := i.listPeripherals(adapterKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Peripherals for %s: %v\n", adapterKey, err)
		pass.ok = false
		return
	}

	known := map[bonds.Address]*Peripheral{}
	for index := range peripherals {
		peripheral := &peripherals[index]
		address, err := bonds.ParseAddress(peripheral.Metadata.Name)
		if err != nil {
			// A Peripheral this operator did not name. Its bond, if it has
			// one, is not addressable, so there is nothing to reconcile it
			// against.
			continue
		}
		known[address] = peripheral
	}

	// Adoption. Every bond bluetoothd holds gets a Peripheral, whether
	// this operator made the bond or found it.
	for _, device := range snapshot.Devices {
		if !device.Paired {
			continue
		}
		if _, found := known[device.Address]; found {
			continue
		}
		peripheral, err := i.createPeripheral(adapter, device, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "adopting the bond with %s: %v\n", device.Address, err)
			pass.ok = false
			continue
		}
		known[device.Address] = peripheral
	}

	for address, peripheral := range known {
		device, present := snapshot.device(address)
		if peripheral.Metadata.deleting() {
			i.unpair(peripheral, address, device, present, pass)
			continue
		}
		if present {
			i.reconcileDeviceSpec(peripheral, device)
		}
		i.writePeripheralStatus(peripheral, adapter, address, device, present, batteries[address], claimed[peripheral.Metadata.Name])
		pass.owners[address] = OwnerReference{
			APIVersion: pairingAPI,
			Kind:       peripheralKind,
			Name:       peripheral.Metadata.Name,
			UID:        peripheral.Metadata.UID,
		}
	}
}

// listPeripherals answers the Peripherals of one radio, from the store
// once the watch for that radio holds its first read.
func (i *inventory) listPeripherals(adapterKey string) ([]Peripheral, error) {
	if held := i.cache.peripheralsOf(adapterKey); held.View.Ready() {
		return informer.CurrentList[Peripheral](i.client, held, peripheralPath)
	}
	list, err := apiclient.Get[PeripheralList](i.client, byAdapter(peripheralsPath(), adapterKey))
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// createPeripheral records a bond in the API. request names the
// PairingRequest that produced the bond, and is empty for a bond the
// operator adopted.
//
// spec.trusted starts from what bluetoothd already holds, so adoption
// states the device's real state rather than asserting a default onto
// hardware that is working. A device the operator paired itself was
// trusted during the peripheral, so its value here is true.
func (i *inventory) createPeripheral(adapter *Adapter, device deviceState, request string) (*Peripheral, error) {
	// The owner reference names the Adapter by its UID. The pass's copy
	// can come from the store, and the store can still hold an Adapter
	// that somebody deleted and this operator created again, whose UID
	// is gone. Garbage collection would then take the new Peripheral and
	// start its unpair. A new Peripheral is rare, so its owner is read
	// from the API server.
	owner, err := informer.ReadFresh[Adapter](i.client, i.cache.adapters.Versions, adapter.Metadata.Name, adapterPath(adapter.Metadata.Name))
	if err != nil {
		return nil, fmt.Errorf("reading the Adapter that owns the bond: %w", err)
	}
	trusted := device.Trusted
	name := device.Address.Key()
	peripheral := &Peripheral{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Metadata: ObjectMeta{
			Name:       name,
			Labels:     map[string]string{bonds.AdapterLabel: adapter.Metadata.Name},
			Finalizers: []string{peripheralFinalizer},
			OwnerReferences: []OwnerReference{{
				APIVersion: pairingAPI,
				Kind:       adapterKind,
				Name:       owner.Metadata.Name,
				UID:        owner.Metadata.UID,
			}},
		},
		Spec: PeripheralSpec{Trusted: &trusted},
	}
	created, err := createObject(i.client, peripheralsPath(), peripheral)
	if err == apiclient.ErrConflict {
		return informer.ReadFresh[Peripheral](i.client, i.cache.peripheralVersions, name, peripheralPath(name))
	}
	if err != nil {
		return nil, err
	}
	i.cache.peripheralVersions.Note(name, created.Metadata.ResourceVersion)
	// pairedAt is when the operator first observed the bond. For a bond it
	// made itself that is the pairing; for one it adopted it is the
	// adoption, because bluetoothd's own storage records no time.
	created.Status.Bond.PairedAt = timestamp(i.now())
	created.Status.Bond.Request = request
	fmt.Printf("peripheral: created %s for the bond with %s\n", name, device.Address)
	return created, nil
}

// reconcileDeviceSpec writes a Peripheral's spec into bluetoothd.
//
// spec.trusted lets the device reconnect on its own: without it BlueZ
// asks an agent to authorize each service on every connection, and no
// agent is registered outside a pairing window. spec.alias is stored
// by bluetoothd in the bond's own info file, so the name is stored in
// the Secret with the keys.
func (i *inventory) reconcileDeviceSpec(peripheral *Peripheral, device deviceState) {
	if peripheral.Spec.Trusted != nil && *peripheral.Spec.Trusted != device.Trusted {
		if err := i.radio.SetDeviceTrusted(device.Address, *peripheral.Spec.Trusted); err != nil {
			fmt.Fprintf(os.Stderr, "setting Trusted on %s: %v\n", device.Address, err)
		} else {
			fmt.Printf("peripheral: %s is now trusted=%t\n", peripheral.Metadata.Name, *peripheral.Spec.Trusted)
		}
	}
	if peripheral.Spec.Alias != "" && peripheral.Spec.Alias != device.Alias {
		if err := i.radio.SetDeviceAlias(device.Address, peripheral.Spec.Alias); err != nil {
			fmt.Fprintf(os.Stderr, "naming %s %q: %v\n", device.Address, peripheral.Spec.Alias, err)
		} else {
			fmt.Printf("peripheral: %s now answers to %q\n", peripheral.Metadata.Name, peripheral.Spec.Alias)
		}
	}
}

// writePeripheralStatus reports the radio's state for one bond.
//
// status.bond.held reports a gap this operator never acts on: a
// Peripheral whose device object is gone from bluetoothd, or is there
// with no bond, is a bond somebody removed by another route. The object
// stays, because deleting it is the unpair API and that is a person's
// act.
//
// pairedAt and bond.request carry over from the object, because the
// radio reports neither. Every other field is this pass's own reading.
//
// kernel is the level the kernel's power supply class reports for this
// device, and nil when it reports none. claimed reports whether a
// prepared claim holds this controller right now.
func (i *inventory) writePeripheralStatus(peripheral *Peripheral, adapter *Adapter, address bonds.Address, device deviceState, present bool, kernel *hidBattery, claimed bool) {
	status := i.peripheralStatus(peripheral.Status, adapter, address, device, present, kernel)

	// The metrics report this pass's reading whether or not it changes
	// what the object holds, because a scrape must see the current
	// state and not only the passes that happened to write it.
	name := peripheral.Metadata.Name
	connected := status.Conditions[0].Status == conditionTrue
	i.metrics.setPeripheralConnected(name, connected)
	i.metrics.setPeripheralClaimed(name, claimed)
	i.metrics.setPeripheralBonded(name, status.Bond.Bonded)
	if status.Battery != nil {
		i.metrics.setPeripheralBattery(name, &status.Battery.Percentage)
	} else {
		i.metrics.setPeripheralBattery(name, nil)
	}

	// replaced is the status the last write went against. A copy from
	// the store can be older than the API server's, so the disconnect
	// and the lost bond are reported from the copy that a landed write
	// replaced, and a stale copy never reports the same change twice.
	var replaced PeripheralStatus
	apply := func(held *Peripheral) bool {
		next := i.peripheralStatus(held.Status, adapter, address, device, present, kernel)
		// The status holds a pointer and a slice, so the comparison is
		// deep.
		if reflect.DeepEqual(held.Status, next) {
			return false
		}
		replaced = held.Status
		held.Status = next
		held.APIVersion, held.Kind = pairingAPI, peripheralKind
		return true
	}
	wrote, err := informer.SettleStatus(i.client, i.cache.peripheralVersions, peripheralPath(name), peripheral, apply)
	if err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		// A Peripheral that is gone needs no status. Its delete event
		// wakes the pass that adopts the bond again.
		fmt.Fprintf(os.Stderr, "writing the status of %s: %v\n", name, err)
	}
	if !wrote {
		return
	}
	// first is true for a Peripheral this write reports on before it
	// ever held a Connected condition, which is a creation or an
	// adoption and never a transition this operator watched happen.
	first := len(replaced.Conditions) == 0
	if !first && connectionWas(replaced.Conditions, conditionTrue) && !connected {
		i.metrics.countDisconnect(name)
	}
	if replaced.Bond.Held && !status.Bond.Held {
		fmt.Fprintf(os.Stderr, "peripheral: %s reports no bond in bluetoothd; the object stays until somebody deletes it\n", name)
	}
}

// peripheralStatus composes one Peripheral's status from this pass's
// reading and the status the object publishes now.
func (i *inventory) peripheralStatus(published PeripheralStatus, adapter *Adapter, address bonds.Address, device deviceState, present bool, kernel *hidBattery) PeripheralStatus {
	status := PeripheralStatus{
		Address: address.Directory(),
		Name:    attributeString(deviceReportedName(device)),
		Icon:    device.Icon,
		Adapter: adapter.Status.Address,
		Node:    adapter.Status.Node,
		Bond: BondStatus{
			Held:      present && device.Paired,
			Paired:    present && device.Paired,
			Bonded:    present && device.Bonded,
			Trusted:   present && device.Trusted,
			Connected: present && device.Connected,
			Secret:    i.namespace + "/" + bonds.BondSecretName(address),
			PairedAt:  published.Bond.PairedAt,
			Request:   published.Bond.Request,
		},
		Conditions: []Condition{
			connectedCondition(published.Conditions, device, present, i.now()),
		},
	}
	if status.Bond.PairedAt == "" {
		status.Bond.PairedAt = timestamp(i.now())
	}
	// The kernel's reading comes first. A controller that states its charge
	// in its HID reports has a power supply and no Battery1, a Low Energy
	// device with a GATT battery service has Battery1 and no power supply,
	// and a device with both is read once, from the kernel, because that
	// reading also carries the charging state.
	switch {
	case kernel != nil:
		status.Battery = &BatteryStatus{
			Percentage: kernel.Percentage,
			Source:     kernel.Name,
			Charging:   kernel.Charging,
		}
	case present && device.Battery != nil:
		status.Battery = &BatteryStatus{
			Percentage: device.Battery.Percentage,
			Source:     device.Battery.Source,
		}
	}
	return status
}

// The Connected condition, and the reasons it carries.
//
// Connected is the link state, in the standard condition shape. The
// reason carries the meaning. LinkUp is a device on the air. Asleep is
// a bonded Low Energy device that drops its link between presses and
// pages the radio again on the next one, and it needs no attention.
// NotConnected is a device that is switched off or out of range.
// NotBonded is a device bluetoothd holds no object for, which means the
// bond was removed by another route.
const (
	conditionConnected = "Connected"

	conditionTrue  = "True"
	conditionFalse = "False"

	reasonLinkUp       = "LinkUp"
	reasonAsleep       = "Asleep"
	reasonNotConnected = "NotConnected"
	reasonNotBonded    = "NotBonded"
)

// connectedCondition reports the link state of one bonded device.
//
// lastTransitionTime carries over from the condition the object already
// holds whenever the status is unchanged, so it marks when the link
// last changed and not when the operator last wrote the object.
func connectedCondition(held []Condition, device deviceState, present bool, now time.Time) Condition {
	condition := Condition{Type: conditionConnected, Status: conditionFalse}
	switch {
	case !present:
		condition.Reason = reasonNotBonded
	case device.Connected:
		condition.Status, condition.Reason = conditionTrue, reasonLinkUp
	case device.Paired && sleepsBetweenSessions(device):
		condition.Reason = reasonAsleep
	default:
		condition.Reason = reasonNotConnected
	}
	condition.LastTransitionTime = timestamp(now)
	for _, previous := range held {
		if previous.Type == condition.Type && previous.Status == condition.Status &&
			previous.LastTransitionTime != "" {
			condition.LastTransitionTime = previous.LastTransitionTime
		}
	}
	return condition
}

// connectionWas reports whether the Connected condition a Peripheral
// held before this pass carried the given status. A Peripheral with
// no Connected condition yet, which is one this pass is adopting or
// creating, answers false.
func connectionWas(held []Condition, status string) bool {
	for _, previous := range held {
		if previous.Type == conditionConnected {
			return previous.Status == status
		}
	}
	return false
}

// hidOverGATT is the assigned number of the HID over GATT service.
const hidOverGATT = 0x1812

// sleepsBetweenSessions reports whether a device that is not connected
// is expected to be off the air.
//
// A Low Energy device drops its link after a short idle and pages the
// radio again on the next press, so disconnected is its resting state.
// Two facts name one: a random address, which only Low Energy uses,
// and the HID over GATT service, which a device on a public address
// can still carry.
func sleepsBetweenSessions(device deviceState) bool {
	if strings.EqualFold(device.AddressType, "random") {
		return true
	}
	for _, uuid := range device.UUIDs {
		if short, ok := shortUUID(uuid); ok && short == hidOverGATT {
			return true
		}
	}
	return false
}

// deviceDisplayName is the name a person recognizes the controller by.
// BlueZ's Name is the device's own broadcast name, and Alias is that
// name until somebody renames the device, so Alias is the better
// choice when the two differ and Name is the fallback for a device
// that has published no name yet.
func deviceDisplayName(device deviceState) string {
	if device.Alias != "" {
		return device.Alias
	}
	return device.Name
}

// deviceReportedName is the name the controller reports for itself,
// which is what status.name promises. Alias is only the
// fallback, for a device that has published no name, where BlueZ
// derives an alias from the address. Without this split, a person who
// sets spec.alias would see their own name in both columns and lose
// the device's.
func deviceReportedName(device deviceState) string {
	if device.Name != "" {
		return device.Name
	}
	return device.Alias
}
