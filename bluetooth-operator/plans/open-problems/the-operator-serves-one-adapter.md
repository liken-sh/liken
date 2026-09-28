# The operator serves one adapter

Open problem. The operator claims one Bluetooth adapter, runs one
bluetoothd, and runs as a `DaemonSet`, one pod per node. A node with two
adapters serves only the adapter the claim took. The second adapter and
its paired controllers publish no device.

## Why one adapter

bluetoothd can manage several adapters at once, so the limit is not in
BlueZ. It is in this operator. Two parts of it are written for one adapter:

* The bond store is per adapter. [Plan 03](../completed/03-a-secret-for-each-adapter.md)
  keys the stored bonds to one adapter's address, and the store reads
  and writes only the tree of that adapter.
* Controller discovery is scoped to the operator's own adapter. The
  operator reads each HID device's `HID_PHYS` field and keeps only the
  devices whose `HID_PHYS` names this adapter, so a second adapter's
  controllers would need a second scope.

A second adapter therefore needs a second `Secret`, a second discovery
scope, and a second set of published controllers. The current code
writes none of them.

## What breaks on a two-adapter node

The claim takes one adapter, and nothing in the claim chooses which.
The scheduler picks. The other adapter is unclaimed and unpublished,
so no device is published for a controller paired to it, and a pod
that selects it stays `Pending`.

## Why the audio operator's approach does not apply directly

The audio operator serves every sound card on its node with
`allocationMode: All`, because one PipeWire serves every ALSA card and
its code already keys every output by card. bluetoothd could span
adapters the same way. But here `allocationMode: All` also requires a
per-adapter `Secret` and a per-adapter discovery scope, and that work
needs a design, not only an edit to the claim.

## What is not measured

No `liken` machine has two Bluetooth adapters today. liken-1 has one.
So nobody has measured the cost of this limit, and any design for two
adapters is a guess until a two-adapter machine runs the operator.
