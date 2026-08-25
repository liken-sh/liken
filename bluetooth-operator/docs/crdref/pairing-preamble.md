A `Pairing` is one bond: this controller holds link keys with this
adapter. The operator creates the object when a pairing succeeds,
and when it finds a bond `bluetoothd` already held. The keys are in
a `Secret` this object owns. Deleting a `Pairing` is the unpair:
the operator disconnects the controller, waits for the claim that
holds it to release, retires the device from the `ResourceSlice`,
and removes the bond from `bluetoothd`, and the `Secret` is
collected with the object. Edit `spec.alias` to rename a controller
and `spec.trusted` to say whether it may reconnect on its own.
