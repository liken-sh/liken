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
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// reconcilePeripherals makes the Peripherals under one Adapter agree
// with the bonds bluetoothd holds.
func (i *inventory) reconcilePeripherals(adapter *Adapter, snapshot radioSnapshot, batteries map[bonds.Address]*hidBattery, pass *inventoryPass) {
	adapterKey := adapter.Metadata.Name
	list, err := get[PeripheralList](i.client, byAdapter(peripheralsPath(), adapterKey))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Peripherals for %s: %v\n", adapterKey, err)
		pass.ok = false
		return
	}

	known := map[bonds.Address]*Peripheral{}
	for index := range list.Items {
		peripheral := &list.Items[index]
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
		i.writePeripheralStatus(peripheral, adapter, address, device, present, batteries[address])
		pass.owners[address] = OwnerReference{
			APIVersion: pairingAPI,
			Kind:       peripheralKind,
			Name:       peripheral.Metadata.Name,
			UID:        peripheral.Metadata.UID,
		}
	}
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
				Name:       adapter.Metadata.Name,
				UID:        adapter.Metadata.UID,
			}},
		},
		Spec: PeripheralSpec{Trusted: &trusted},
	}
	created, err := createObject(i.client, peripheralsPath(), peripheral)
	if err == ErrConflict {
		return get[Peripheral](i.client, peripheralPath(name))
	}
	if err != nil {
		return nil, err
	}
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
// device, and nil when it reports none.
func (i *inventory) writePeripheralStatus(peripheral *Peripheral, adapter *Adapter, address bonds.Address, device deviceState, present bool, kernel *hidBattery) {
	status := PeripheralStatus{
		Address: address.Directory(),
		Name:    attributeString(deviceReportedName(device)),
		Icon:    device.Icon,
		Adapter: adapter.Status.Address,
		Node:    adapter.Status.Node,
		Bond: BondStatus{
			Held:     present && device.Paired,
			Secret:   i.namespace + "/" + bonds.BondSecretName(address),
			PairedAt: peripheral.Status.Bond.PairedAt,
			Request:  peripheral.Status.Bond.Request,
		},
		Conditions: []Condition{
			connectedCondition(peripheral.Status.Conditions, device, present, i.now()),
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
	// The status holds a pointer and a slice, so the comparison is deep.
	if reflect.DeepEqual(peripheral.Status, status) {
		return
	}
	if peripheral.Status.Bond.Held && !status.Bond.Held {
		fmt.Fprintf(os.Stderr, "peripheral: %s reports no bond in bluetoothd; the object stays until somebody deletes it\n",
			peripheral.Metadata.Name)
	}
	peripheral.Status = status
	peripheral.APIVersion, peripheral.Kind = pairingAPI, peripheralKind
	if err := replaceStatus(i.client, peripheralPath(peripheral.Metadata.Name), peripheral); err != nil {
		fmt.Fprintf(os.Stderr, "writing the status of %s: %v\n", peripheral.Metadata.Name, err)
	}
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
