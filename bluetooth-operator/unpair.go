package main

// Deleting a Peripheral unpairs the controller. The teardown runs one
// step per pass, and each step must finish before the next starts,
// because a device that a claim still holds must stay in the
// ResourceSlice:
//
//  1. Disconnect the device. The next pass adds the disconnected taint
//     to the device in the slice, and the taint-eviction controller
//     evicts the pod after its tolerationSeconds.
//  2. Wait until no prepared claim holds the controller. The kubelet
//     calls unprepare after the pod's containers have stopped, which
//     releases the claim.
//  3. Remove the device from the ResourceSlice and stop its input
//     relay, so that no new claim can be allocated to a bond that is
//     about to go and no virtual node outlives it.
//  4. Remove the bond from bluetoothd and remove the finalizer. The
//     Peripheral owns the Secret that holds the keys, so garbage
//     collection deletes the Secret with it.
//
// Steps 3 and 4 run in separate passes. The publisher writes the slice
// after the pass that runs step 3, so the slice no longer lists the
// device when step 4 starts.

import (
	"errors"
	"fmt"
	"os"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// unpair runs at most one step of the teardown of a Peripheral.
func (i *inventory) unpair(peripheral *Peripheral, address bonds.Address, device deviceState, present bool, pass *inventoryPass) {
	name := peripheral.Metadata.Name
	// Read the Peripheral from the API server and not from the cache.
	// The cache can still hold a Peripheral whose finalizer an earlier
	// pass removed, and a teardown from that copy would remove a
	// device that no Peripheral names any more from the slice.
	current, err := informer.ReadFresh[Peripheral](i.client, i.cache.peripheralVersions, name, peripheralPath(name))
	if errors.Is(err, apiclient.ErrNotFound) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "unpair %s: reading the object: %v\n", name, err)
		pass.ok = false
		return
	}
	peripheral = current
	if !peripheral.Metadata.hasFinalizer(peripheralFinalizer) {
		// Without the finalizer the API server deletes the object, so
		// there is nothing left to tear down.
		return
	}
	// The rest of the pass neither reconnects a bond that is being
	// unpaired nor writes its Secret again. The Secret stays until
	// garbage collection deletes it with the Peripheral.
	pass.unpairing[address] = true
	pass.runAgainIn(followUpDelay)

	// The publisher builds each slice from the current pass alone, so
	// every pass must leave a removed device out again. Otherwise the
	// device would return to the slice between step 3 and step 4.
	if i.removed[address] {
		i.removeFromSlice(address, pass)
	}

	if present && device.Connected {
		if err := i.radio.Disconnect(address); err != nil {
			fmt.Fprintf(os.Stderr, "unpair %s: disconnecting the device: %v\n", name, err)
			pass.ok = false
			return
		}
		fmt.Printf("unpair %s: disconnected the device, and the unpair waits for the claim to release it\n", name)
		return
	}

	if claimedDevices()[address.Key()] {
		// A pod still holds this controller. The device stays in the
		// slice until the kubelet unprepares the claim, because an
		// allocation for a device that is in no slice makes the
		// kubelet's prepare call fail and retry. The taint on the device
		// evicts the pod.
		fmt.Printf("unpair %s: a prepared claim still holds the device, so the unpair waits\n", name)
		return
	}

	if !i.removed[address] {
		// No claim holds the device, so take it out of the slice and
		// stop its relay. The relay's virtual node exists only for
		// claims. The publisher starts relays only for devices it
		// publishes, so the relay does not start again.
		fmt.Printf("unpair %s: removing the device from the ResourceSlice\n", name)
		i.relays.stop(macFromDeviceName(address.Key()))
		i.removeFromSlice(address, pass)
		return
	}

	if err := i.radio.Remove(address); err != nil {
		fmt.Fprintf(os.Stderr, "unpair %s: removing the bond from bluetoothd: %v\n", name, err)
		pass.ok = false
		return
	}
	version, err := patchFinalizers(i.client, peripheralPath(name), peripheral.Metadata.ResourceVersion,
		peripheral.Metadata.withoutFinalizer(peripheralFinalizer))
	if err != nil {
		fmt.Fprintf(os.Stderr, "unpair %s: removing the finalizer from the Peripheral: %v\n", name, err)
		pass.ok = false
		return
	}
	i.cache.peripheralVersions.Note(name, version)
	delete(i.removed, address)
	delete(i.removing, address)
	// With the finalizer gone, the API server deletes the Peripheral.
	// Delete its metric series too, or they keep reporting their last
	// values.
	i.metrics.forgetPeripheral(name)
	fmt.Printf("unpair %s: removed the bond and the finalizer, so the API server deletes the Peripheral and its Secret\n", name)
}
