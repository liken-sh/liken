A `Peripheral` is one bonded device: this controller or speaker
holds link keys with this adapter, and the object reports what the
radio observes about it. The operator creates the object when a
pairing succeeds, and when it finds a bond `bluetoothd` already
held. The keys are in a `Secret` this object owns. Read
`status.conditions` for the link, `status.battery` for the charge
the device reports, and `status.bond` for the keys. Deleting a
`Peripheral` is the unpair: the operator disconnects the device,
waits for the claim that holds it to release, retires the device
from the `ResourceSlice`, and removes the bond from `bluetoothd`,
and the `Secret` is collected with the object. Edit `spec.alias` to
rename a device and `spec.trusted` to say whether it may reconnect
on its own.
