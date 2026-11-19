# `status.bond.held` copies `status.bond.paired`

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`peripheralStatus` in `peripheral.go` sets both fields from the same
expression:

```go
Held:      present && device.Paired,
Paired:    present && device.Paired,
```

So the two fields of a `Peripheral` always have the same value, and
`held` gives a person no fact that `paired` does not already give.

## Evidence

- `deploy/crds.yaml` describes `held` as "Whether bluetoothd still has
  this bond", false "when bluetoothd no longer has the keys". It
  describes `paired` as BlueZ's `Device1.Paired`, and `bonded` as "a
  stored link key for this device". By those descriptions, `held`
  follows the keys, which is `Device1.Bonded`, or the presence of the
  device object, which is `present`.
- `crd.go` documents `Held` as "whether bluetoothd still has the
  bond", and documents `Paired`, `Bonded`, `Trusted`, and `Connected`
  as copies of the BlueZ properties.
- `writePeripheralStatus` posts the `BondLost` `Warning` when `held`
  goes from true to false. With the current expression, a device that
  BlueZ still reports as present but no longer as paired also posts
  `BondLost`, and a device that loses its link key but stays paired
  posts nothing.
- Plan 06 introduced `held` before the BlueZ properties were copied
  into the status, so the expression may date from a time when
  `Paired` was the only property read.

## The reproduction

The test ran a reconcile pass over the fake API server and the fake
radio with a paired controller, then changed the controller in the
fake radio to `Paired` and not `Bonded`, which the CRD says is
possible ("A device can be paired without a stored key"), and ran a
second pass. It asserted that `status.bond.held` is false, as the CRD
describes, and it failed with:

    status.bond = {Held:true Paired:true Bonded:false Trusted:true Connected:true Secret:liken-system/bluetooth-bond-a0-ab-51-33-b7-12 PairedAt:2026-08-17T17:30:00Z Request:}, want held false because bluetoothd stores no key for the device

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

What does `status.bond.held` tell a person, and when does the
`BondLost` warning fire? The options are that bluetoothd still has
the device, that it stores a link key for it, or that it is paired.
A last option removes `held`.

- Which BlueZ fact `held` reports: the device object (`present`), the
  stored key (`Bonded`), or the pairing (`Paired`).
- Whether `BondLost` follows the same fact, and whether the manual's
  `Event` table and the `kubectl get peripherals` columns change with
  it.
- Whether `held` stays at all, when it reports the same fact as
  another field.
