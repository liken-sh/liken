package main

// A Peripheral records one device's bond with one adapter. Each pass
// creates a Peripheral, owned by the Adapter, for every bond in
// bluetoothd that has none, whether this operator paired the device or
// found the bond already there. When a bond disappears from bluetoothd,
// its Peripheral stays and the status reports the missing bond. The
// operator does not delete the Peripheral, because deleting it unpairs
// the device, and only a person should decide that.

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// reconcilePeripherals makes the Peripherals of one Adapter match the
// bonds in bluetoothd. claimed is keyed by Peripheral name and is true
// for each controller that a prepared claim holds.
func (i *inventory) reconcilePeripherals(adapter *Adapter, snapshot radioSnapshot, batteries map[bonds.Address]*hidBattery, claimed map[string]bool, pass *inventoryPass) {
	adapterKey := adapter.Metadata.Name
	peripherals, err := i.listPeripherals(adapterKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Peripherals of Adapter %s: %v\n", adapterKey, err)
		pass.ok = false
		return
	}

	known := map[bonds.Address]*Peripheral{}
	for index := range peripherals {
		peripheral := &peripherals[index]
		address, err := bonds.ParseAddress(peripheral.Metadata.Name)
		if err != nil {
			// The name is not a device address, so this operator did not
			// create the Peripheral and has no bond to match it with.
			continue
		}
		known[address] = peripheral
	}

	for _, device := range snapshot.Devices {
		if !device.Paired {
			continue
		}
		if _, found := known[device.Address]; found {
			continue
		}
		peripheral, err := i.createPeripheral(adapter, device, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "adopting the bond with device %s: %v\n", device.Address, err)
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

// listPeripherals returns the Peripherals of one adapter. It reads the
// watch's cache once the watch has completed its first list, and the
// API server before that.
func (i *inventory) listPeripherals(adapterKey string) ([]Peripheral, error) {
	return informer.List[Peripheral](i.client, i.cache.peripheralsOf(adapterKey), byAdapter(peripheralsPath(), adapterKey), peripheralPath)
}

// createPeripheral creates the Peripheral for a bond. request is the
// name of the PairingRequest that made the bond, and is empty for a
// bond that already existed.
//
// spec.trusted starts as the device's current Trusted property, so
// adopting a bond does not change the device. The pairing sets Trusted
// on a device that this operator paired, so its Peripheral starts with
// true.
func (i *inventory) createPeripheral(adapter *Adapter, device deviceState, request string) (*Peripheral, error) {
	// The owner reference needs the Adapter's current UID. The cache can
	// still hold an Adapter that was deleted and created again, with its
	// old UID, and the garbage collector deletes a Peripheral whose owner
	// UID does not exist, which unpairs the device. Creating a Peripheral
	// is rare, so read the Adapter from the API server.
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
	// bluetoothd does not record when a bond was made, so pairedAt is the
	// time this operator first saw the bond: the pairing time for a
	// device it paired, and the adoption time for any other.
	created.Status.Bond.PairedAt = timestamp(i.now())
	created.Status.Bond.Request = request
	fmt.Printf("peripheral: created Peripheral %s for the bond with device %s\n", name, device.Address)
	return created, nil
}

// reconcileDeviceSpec sets the device's Trusted and Alias properties in
// bluetoothd to the values in the Peripheral's spec.
//
// A device must be trusted to reconnect on its own, because BlueZ asks
// an agent to authorize each service of an untrusted device on every
// connection, and no agent is registered outside a pairing window.
// bluetoothd writes the alias into the bond's info file, and the bond's
// Secret stores that file, so the alias is restored with the keys.
func (i *inventory) reconcileDeviceSpec(peripheral *Peripheral, device deviceState) {
	if peripheral.Spec.Trusted != nil && *peripheral.Spec.Trusted != device.Trusted {
		if err := i.radio.SetDeviceTrusted(device.Address, *peripheral.Spec.Trusted); err != nil {
			fmt.Fprintf(os.Stderr, "setting Trusted on device %s: %v\n", device.Address, err)
		} else {
			fmt.Printf("peripheral: set Trusted of %s to %t\n", peripheral.Metadata.Name, *peripheral.Spec.Trusted)
		}
	}
	if peripheral.Spec.Alias != "" && peripheral.Spec.Alias != device.Alias {
		if err := i.radio.SetDeviceAlias(device.Address, peripheral.Spec.Alias); err != nil {
			fmt.Fprintf(os.Stderr, "setting the alias of device %s to %q: %v\n", device.Address, peripheral.Spec.Alias, err)
		} else {
			fmt.Printf("peripheral: set the alias of %s to %q\n", peripheral.Metadata.Name, peripheral.Spec.Alias)
		}
	}
}

// writePeripheralStatus writes one Peripheral's status from the state
// of its device in bluetoothd, sets the Peripheral's metrics, and posts
// an Event for each change of the Connected condition and for a lost
// bond. kernel is the battery reading from the kernel's power supply
// class, or nil when there is none. claimed is true when a prepared
// claim holds this controller.
//
// The reason the device's last link ended is read once, so a status
// built again after a conflict gets the same condition. It is cleared
// only when the write succeeds or the status is already current, so a
// failed write leaves it for the next pass.
func (i *inventory) writePeripheralStatus(peripheral *Peripheral, adapter *Adapter, address bonds.Address, device deviceState, present bool, kernel *hidBattery, claimed bool) {
	ended := i.ends.reason(address)
	status := i.peripheralStatus(peripheral.Status, adapter, address, device, present, kernel, ended)

	// Set the metrics on every pass, even when the status write is
	// skipped, so a scrape always shows the current state.
	name := peripheral.Metadata.Name
	i.metrics.setPeripheralConnected(name, status.Conditions[0].Status == conditionTrue)
	i.metrics.setPeripheralClaimed(name, claimed)
	i.metrics.setPeripheralBonded(name, status.Bond.Bonded)
	if status.Battery != nil {
		i.metrics.setPeripheralBattery(name, &status.Battery.Percentage)
	} else {
		i.metrics.setPeripheralBattery(name, nil)
	}

	// The cached copy can be older than the API server's copy, and can
	// still show a change that an earlier write already reported. So the
	// disconnect count and the Events compare the new status against
	// replaced, the status that the successful write overwrote, and not
	// against the cached copy. settled is the status the object has
	// after this call, whether or not the call wrote it.
	var replaced, settled PeripheralStatus
	apply := func(current *Peripheral) bool {
		next := i.peripheralStatus(current.Status, adapter, address, device, present, kernel, ended)
		settled = next
		// Status contains a pointer and a slice, so == does not work.
		if reflect.DeepEqual(current.Status, next) {
			return false
		}
		replaced = current.Status
		current.Status = next
		current.APIVersion, current.Kind = pairingAPI, peripheralKind
		return true
	}
	wrote, err := informer.SettleStatus(i.client, i.cache.peripheralVersions, peripheralPath(name), peripheral, apply)
	if err != nil {
		// A deleted Peripheral needs no status. The watch event of the
		// delete starts a pass, which creates the Peripheral again if
		// the bond is still in bluetoothd.
		if !errors.Is(err, apiclient.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "writing the status of Peripheral %s: %v\n", name, err)
		}
		return
	}
	i.ends.forget(address, ended)
	// The relay schedules the wake press from the Connected condition
	// (wake.go). It gets the settled condition even when this call wrote
	// nothing, so the first pass after an operator restart schedules the
	// press for a remote whose Peripheral already has the reason Asleep.
	connection := settled.Conditions[0]
	i.relays.follow(macFromDeviceName(address.Key()), connection)
	if !wrote {
		return
	}
	// A new Peripheral has no Connected condition before this write. Its
	// first condition posts an Event and is not counted as a disconnect,
	// because the operator did not see the link go down.
	previous, had := conditions.Find(replaced.Conditions, conditionConnected)
	if had && previous.Status == conditionTrue && connection.Status == conditionFalse {
		i.metrics.countDisconnect(name, connection.Reason)
	}
	if !had || previous.Status != connection.Status || previous.Reason != connection.Reason {
		i.recorder.Transition(peripheralReference(peripheral), connection, connectedWarning(connection.Reason))
	}
	if replaced.Bond.Held && !status.Bond.Held {
		fmt.Fprintf(os.Stderr, "peripheral: bluetoothd has no bond for %s, and the Peripheral stays until a person deletes it\n", name)
		i.recorder.Warning(peripheralReference(peripheral), reasonBondLost, fmt.Sprintf(
			"bluetoothd has no bond with %s. Delete the Peripheral to unpair it, or pair the device again", address))
	}
}

// peripheralStatus builds a Peripheral's status from the device's
// current state and the reason its last link ended. published is the
// object's current status, and supplies the fields that bluetoothd does
// not report: pairedAt, bond.request, and the condition's
// lastTransitionTime.
func (i *inventory) peripheralStatus(published PeripheralStatus, adapter *Adapter, address bonds.Address, device deviceState, present bool, kernel *hidBattery, ended string) PeripheralStatus {
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
			connectedCondition(published.Conditions, device, present, ended, i.now()),
		},
	}
	if status.Bond.PairedAt == "" {
		status.Bond.PairedAt = timestamp(i.now())
	}
	// A controller that reports its charge in HID reports gets a kernel
	// power supply and no BlueZ Battery1. A Low Energy device with a GATT
	// battery service gets Battery1 and no power supply. When a device
	// has both, the kernel's reading wins, because it also reports
	// whether the battery is charging.
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

// deviceDisplayName returns the name to show a person. BlueZ returns
// the device's own Name as Alias until someone sets an alias, so Alias
// is the better choice when it is set.
func deviceDisplayName(device deviceState) string {
	if device.Alias != "" {
		return device.Alias
	}
	return device.Name
}

// deviceReportedName returns the name for status.name: the name the
// controller reports for itself. It prefers Name over Alias, because
// spec.alias sets the alias, and the device's own name would otherwise
// appear nowhere. A device that has reported no name gets Alias, which
// BlueZ then derives from the address.
func deviceReportedName(device deviceState) string {
	if device.Name != "" {
		return device.Name
	}
	return device.Alias
}
