package main

// The Events this operator posts, for a person who runs
// `kubectl describe` on a PairingRequest, a Peripheral, an Adapter, or
// a Node.
//
// An Event records something that just happened, such as a pairing
// window opening, a link ending, or the input recovery powering the
// adapter off and on. The API server deletes an Event an hour after its
// last write, so the state each Event reports is also in a status field
// or a log line.
//
// Each transition of a Peripheral's Connected condition posts an Event
// with the condition's reason and message (connected.go).
// AuthenticationFailed is a Warning
// because a person must pair the device again. Every other reason is
// Normal. The key press that the relay writes when a remote wakes posts
// no Event (wake.go), because the condition change to LinkUp already
// posts one.
//
// Peripherals, Adapters, and Nodes are cluster-scoped, so their Events
// are written in `default`. A PairingRequest's Events are written in
// its own namespace.

import "github.com/liken-sh/liken/kubernetes/events"

// The Event reasons this operator posts. The install guide lists them,
// so renaming one changes the published interface. connected.go
// declares the reasons of the Connected condition, which are also
// posted as Events.
const (
	// reasonPairingWindowOpened is Normal, on a PairingRequest: the
	// radio is discoverable and pairable until the window closes.
	reasonPairingWindowOpened = "PairingWindowOpened"

	// reasonPairingWindowExpired is Normal, on a PairingRequest: the
	// window closed and nobody approved a device the radio observed.
	reasonPairingWindowExpired = "PairingWindowExpired"

	// reasonPairingRefused is a Warning, on a PairingRequest:
	// bluetoothd refused to pair the approved device. The window tries
	// again on each pass, and the Event is posted only when the refusal
	// first appears or its message changes.
	reasonPairingRefused = "PairingRefused"

	// reasonPaired is Normal, on the PairingRequest and on the
	// Peripheral it created: the device is bonded with the radio.
	reasonPaired = "Paired"

	// reasonBondLost is a Warning, on a Peripheral: bluetoothd no
	// longer has a bond with the device, so something else removed it.
	// The Peripheral remains until a person deletes it.
	reasonBondLost = "BondLost"

	// reasonInputRelayFailed is a Warning, on a Peripheral: the
	// operator could not create the virtual input device that a claim
	// on the controller gets.
	reasonInputRelayFailed = "InputRelayFailed"

	// reasonAdapterPowerCycled is Normal, on a Peripheral: bluetoothd
	// failed to disconnect the controller during input recovery, so the
	// operator powered the adapter off and on. That dropped every link
	// on the adapter (inputrecovery.go).
	reasonAdapterPowerCycled = "AdapterPowerCycled"

	// reasonAdapterPowerOnFailed is a Warning, on a Peripheral: input
	// recovery powered the adapter off, and bluetoothd failed to power
	// it on again. The adapter stays off until bluetoothd restarts with
	// the pod.
	reasonAdapterPowerOnFailed = "AdapterPowerOnFailed"

	// reasonRadioClaimed is Normal, on the Node: bluetoothd in this
	// pod reports a radio, and the operator publishes it.
	reasonRadioClaimed = "RadioClaimed"

	// reasonRadioLost is a Warning, on the Node: bluetoothd stopped
	// reporting a radio it had reported, for example because the adapter
	// was unplugged or reset.
	reasonRadioLost = "RadioLost"

	// reasonPrivacyChanged is Normal, on an Adapter: spec.privacy
	// differs from the value bluetoothd started with, so the operator
	// deletes its own pod and bluetoothd restarts with the new value.
	reasonPrivacyChanged = "PrivacyChanged"

	// reasonBtmonChanged is Normal, on an Adapter: spec.btmon differs
	// from the value in the settings volume, so the operator wrote the
	// new value and the btmon container starts or stops its trace.
	reasonBtmonChanged = "BtmonChanged"
)

// adapterReference names an Adapter in an Event.
func adapterReference(adapter *Adapter) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       adapterKind,
		Name:       adapter.Metadata.Name,
		UID:        adapter.Metadata.UID,
	}
}

// requestReference names a PairingRequest in an Event.
func requestReference(request *PairingRequest) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       pairingRequestKind,
		Namespace:  request.Metadata.Namespace,
		Name:       request.Metadata.Name,
		UID:        request.Metadata.UID,
	}
}

// peripheralReference names a Peripheral in an Event.
func peripheralReference(peripheral *Peripheral) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       peripheral.Metadata.Name,
		UID:        peripheral.Metadata.UID,
	}
}

// controllerReference refers to a controller's Peripheral by address
// alone, for code that has no Peripheral object. Without a UID, the
// recorder identifies the object by kind and name.
func controllerReference(mac string) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: pairingAPI,
		Kind:       peripheralKind,
		Name:       deviceName(mac),
	}
}

// nodeReference refers to the Node this operator runs on, using the
// owner reference of its ResourceSlice.
func nodeReference(owner OwnerReference) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: owner.APIVersion,
		Kind:       owner.Kind,
		Name:       owner.Name,
		UID:        owner.UID,
	}
}
