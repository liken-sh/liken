A `Peripheral` is one controller or speaker that has paired with an
adapter and shares link keys with it. The operator creates the
`Peripheral` when pairing succeeds, and when it finds a bond that
`bluetoothd` already stores. The keys are in a `Secret` that the
`Peripheral` owns. Read `status.conditions` for the link,
`status.battery` for the charge the device reports, and `status.bond`
for the keys. Edit `spec.alias` to rename a device, and
`spec.trusted` to set whether it may reconnect on its own.

To unpair a device, delete its `Peripheral`. The operator disconnects
the device, waits until no claim holds it, removes the device from
the `ResourceSlice`, and deletes the bond from `bluetoothd`.
Kubernetes deletes the `Secret` with the `Peripheral`.
