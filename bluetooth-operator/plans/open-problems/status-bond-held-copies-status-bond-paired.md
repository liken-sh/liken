# `status.bond.held` copies `status.bond.paired`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

A unit test of `peripheralStatus` with a device that BlueZ reports as
`Paired` and not `Bonded`, which the CRD says is possible ("A device
can be paired without a stored key"). The test shows whether `held`
then reports the keys that the CRD promises.

## What a fix has to decide

- Which BlueZ fact `held` reports: the device object (`present`), the
  stored key (`Bonded`), or the pairing (`Paired`).
- Whether `BondLost` follows the same fact, and whether the manual's
  `Event` table and the `kubectl get peripherals` columns change with
  it.
- Whether `held` stays at all, when it reports the same fact as
  another field.
