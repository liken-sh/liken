package main

// Deleting a Pairing is the unpair API, and an unpair has an order.
//
// The rule every device operator here follows is that a device a claim
// still names never leaves the published inventory. So the teardown
// works from the consumer inwards, one step for each pass, and each
// step is checked before the next one runs:
//
//  1. Disconnect the device. The session ends, the controller
//     registers no evdev node, and the ordinary reconcile puts both
//     taints on the slice device. The eviction controller acts on the
//     NoExecute taint, and the consumer's own tolerationSeconds sets
//     how long that takes.
//  2. Wait until no prepared claim holds the controller. The kubelet's
//     unprepare call ends the claim's hold, and it runs after the
//     consumer's container is gone.
//  3. Retire the device from the ResourceSlice, so no new claim can
//     be allocated to a bond that is about to go.
//  4. Remove the bond from bluetoothd, and release the finalizer.
//     The Secret that holds the keys is owned by the Pairing, so garbage
//     collection takes it in the same act.
//
// Steps 3 and 4 are separated by a pass on purpose. The publisher
// writes the slice after this runs, so the device is out of the
// published inventory before the pass that removes the bond starts.

import (
	"fmt"
	"os"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// unpair advances one Pairing's teardown by at most one step.
func (i *inventory) unpair(pairing *Pairing, address bonds.Address, device deviceState, present bool, pass *inventoryPass) {
	name := pairing.Metadata.Name
	if !pairing.Metadata.holds(pairingFinalizer) {
		// Nothing holds the object. The API server removes it as soon as
		// the last finalizer is gone, so there is no teardown to run.
		return
	}
	// The bond is being removed, so its Secret is not rewritten while
	// the teardown runs. The Secret itself stays until the Pairing that
	// owns it is collected.
	pass.unpairing[address] = true
	pass.runAgainIn(followUpDelay)

	// A device that has already left the slice stays out of it. The
	// publisher builds each slice from this pass alone, so a teardown
	// must repeat the exclusion on every pass, or the device would be
	// published once more between two of its own steps.
	if i.retired[address] {
		i.retire(address, pass)
	}

	if present && device.Connected {
		if err := i.radio.Disconnect(address); err != nil {
			fmt.Fprintf(os.Stderr, "unpair %s: disconnecting: %v\n", name, err)
			pass.ok = false
			return
		}
		fmt.Printf("unpair %s: disconnected; waiting for the claim to release it\n", name)
		return
	}

	if claimedDevices()[address.Key()] {
		// A consumer still holds this controller. The device stays in the
		// slice while that is true, because an allocation that names a
		// device in no slice strands the kubelet's prepare call. The
		// taints on the device end that pod, and this waits for the
		// unprepare that follows.
		fmt.Printf("unpair %s: a prepared claim still holds it; waiting\n", name)
		return
	}

	if !i.retired[address] {
		// The claim is released, so the device leaves the published
		// inventory. The publisher runs after this pass, and the next
		// pass removes the bond.
		fmt.Printf("unpair %s: retiring it from the slice\n", name)
		i.retire(address, pass)
		return
	}

	if err := i.radio.Remove(address); err != nil {
		fmt.Fprintf(os.Stderr, "unpair %s: removing the bond: %v\n", name, err)
		pass.ok = false
		return
	}
	if _, err := patchFinalizers(i.client, pairingPath(name), pairing.Metadata.ResourceVersion,
		pairing.Metadata.without(pairingFinalizer)); err != nil {
		fmt.Fprintf(os.Stderr, "unpair %s: releasing the object: %v\n", name, err)
		pass.ok = false
		return
	}
	delete(i.retired, address)
	delete(i.retiring, address)
	fmt.Printf("unpair %s: the bond is gone and the Secret goes with the object\n", name)
}
