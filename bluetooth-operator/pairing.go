package main

// The Pairing object: the durable fact that one device holds a bond
// with one adapter.
//
// Adoption runs on every pass, in both directions. A bond in
// bluetoothd with no Pairing gets one created, owned by the Adapter. A
// Pairing whose bond is gone from bluetoothd keeps its object and
// reports the gap in status, because deleting a Pairing means unpair,
// and that is a person's decision and not an operator's.
//
// The same code handles the migration. A machine that paired its
// controllers before this API existed holds bonds in bluetoothd and no
// objects at all, and the first pass of the new operator creates a
// Pairing for each one and a Secret under each Pairing. There is no
// separate migration path to keep working.

import (
	"fmt"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// reconcilePairings makes the Pairings under one Adapter agree with
// the bonds bluetoothd holds.
func (i *inventory) reconcilePairings(adapter *Adapter, snapshot radioSnapshot, pass *inventoryPass) {
	adapterKey := adapter.Metadata.Name
	list, err := get[PairingList](i.client, byAdapter(pairingsPath(), adapterKey))
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing the Pairings for %s: %v\n", adapterKey, err)
		pass.ok = false
		return
	}

	known := map[bonds.Address]*Pairing{}
	for index := range list.Items {
		pairing := &list.Items[index]
		address, err := bonds.ParseAddress(pairing.Metadata.Name)
		if err != nil {
			// A Pairing this operator did not name. Its bond, if it has
			// one, is not addressable, so there is nothing to reconcile it
			// against.
			continue
		}
		known[address] = pairing
	}

	// Adoption. Every bond bluetoothd holds gets a Pairing, whether this
	// operator made the bond or found it.
	for _, device := range snapshot.Devices {
		if !device.Paired {
			continue
		}
		if _, found := known[device.Address]; found {
			continue
		}
		pairing, err := i.createPairing(adapter, device, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "adopting the bond with %s: %v\n", device.Address, err)
			pass.ok = false
			continue
		}
		known[device.Address] = pairing
	}

	for address, pairing := range known {
		device, present := snapshot.device(address)
		if pairing.Metadata.deleting() {
			i.unpair(pairing, address, device, present, pass)
			continue
		}
		if present {
			i.reconcileDeviceSpec(pairing, device)
		}
		i.writePairingStatus(pairing, adapter, address, device, present)
		pass.owners[address] = OwnerReference{
			APIVersion: pairingAPI,
			Kind:       pairingKind,
			Name:       pairing.Metadata.Name,
			UID:        pairing.Metadata.UID,
		}
	}
}

// createPairing records a bond in the API. request names the
// PairingRequest that produced the bond, and is empty for a bond the
// operator adopted.
//
// spec.trusted starts from what bluetoothd already holds, so adoption
// states the device's real state rather than asserting a default onto
// hardware that is working. A device the operator paired itself was
// trusted during the pairing, so its value here is true.
func (i *inventory) createPairing(adapter *Adapter, device deviceState, request string) (*Pairing, error) {
	trusted := device.Trusted
	name := device.Address.Key()
	pairing := &Pairing{
		APIVersion: pairingAPI,
		Kind:       pairingKind,
		Metadata: ObjectMeta{
			Name:       name,
			Labels:     map[string]string{bonds.AdapterLabel: adapter.Metadata.Name},
			Finalizers: []string{pairingFinalizer},
			OwnerReferences: []OwnerReference{{
				APIVersion: pairingAPI,
				Kind:       adapterKind,
				Name:       adapter.Metadata.Name,
				UID:        adapter.Metadata.UID,
			}},
		},
		Spec: PairingSpec{Trusted: &trusted},
	}
	created, err := createObject(i.client, pairingsPath(), pairing)
	if err == ErrConflict {
		return get[Pairing](i.client, pairingPath(name))
	}
	if err != nil {
		return nil, err
	}
	// pairedAt is when the operator first observed the bond. For a bond it
	// made itself that is the pairing; for one it adopted it is the
	// adoption, because bluetoothd's own storage records no time.
	created.Status.PairedAt = timestamp(i.now())
	created.Status.Request = request
	fmt.Printf("pairing: created %s for the bond with %s\n", name, device.Address)
	return created, nil
}

// reconcileDeviceSpec carries a Pairing's spec into bluetoothd.
//
// spec.trusted is what lets the device reconnect on its own: without
// it BlueZ asks an agent to authorize each service on every
// connection, and no agent is registered outside a pairing window.
// spec.alias is stored by bluetoothd in the bond's own info file, so
// the name is stored in the Secret with the keys.
func (i *inventory) reconcileDeviceSpec(pairing *Pairing, device deviceState) {
	if pairing.Spec.Trusted != nil && *pairing.Spec.Trusted != device.Trusted {
		if err := i.radio.SetDeviceTrusted(device.Address, *pairing.Spec.Trusted); err != nil {
			fmt.Fprintf(os.Stderr, "setting Trusted on %s: %v\n", device.Address, err)
		} else {
			fmt.Printf("pairing: %s is now trusted=%t\n", pairing.Metadata.Name, *pairing.Spec.Trusted)
		}
	}
	if pairing.Spec.Alias != "" && pairing.Spec.Alias != device.Alias {
		if err := i.radio.SetDeviceAlias(device.Address, pairing.Spec.Alias); err != nil {
			fmt.Fprintf(os.Stderr, "naming %s %q: %v\n", device.Address, pairing.Spec.Alias, err)
		} else {
			fmt.Printf("pairing: %s now answers to %q\n", pairing.Metadata.Name, pairing.Spec.Alias)
		}
	}
}

// writePairingStatus reports the radio's state for one bond.
//
// status.bonded reports a gap this operator never acts on: a Pairing
// whose device object is gone from bluetoothd, or is there with no
// bond, is a bond somebody removed by another route. The
// object stays, because deleting it is the unpair API and that is a
// person's act.
func (i *inventory) writePairingStatus(pairing *Pairing, adapter *Adapter, address bonds.Address, device deviceState, present bool) {
	status := PairingStatus{
		Address:    address.Directory(),
		DeviceName: attributeString(deviceReportedName(device)),
		Adapter:    adapter.Status.Address,
		Node:       adapter.Status.Node,
		Connected:  present && device.Connected,
		Bonded:     present && device.Paired,
		Secret:     i.namespace + "/" + bonds.BondSecretName(address),
		PairedAt:   pairing.Status.PairedAt,
		Request:    pairing.Status.Request,
	}
	if status.PairedAt == "" {
		status.PairedAt = timestamp(i.now())
	}
	if pairing.Status == status {
		return
	}
	if pairing.Status.Bonded && !status.Bonded {
		fmt.Fprintf(os.Stderr, "pairing: %s reports no bond in bluetoothd; the object stays until somebody deletes it\n",
			pairing.Metadata.Name)
	}
	pairing.Status = status
	pairing.APIVersion, pairing.Kind = pairingAPI, pairingKind
	if err := replaceStatus(i.client, pairingPath(pairing.Metadata.Name), pairing); err != nil {
		fmt.Fprintf(os.Stderr, "writing the status of %s: %v\n", pairing.Metadata.Name, err)
	}
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
// which is what status.deviceName promises. Alias is only the
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
