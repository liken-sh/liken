# The operator serves one adapter

Open problem. The operator claims one Bluetooth adapter, runs one
bluetoothd, and runs as a DaemonSet, one pod per node. A node with two
adapters serves only the adapter the claim took. The second adapter and
its paired controllers publish no device.

## Why one adapter

bluetoothd can manage several adapters at once, so the limit is not
BlueZ. It is this operator. Two parts of it are written for one
adapter:

* The bond store is per adapter. [Plan 03](../completed/03-a-secret-for-each-adapter.md)
  keeps one Secret that holds the bonds of one adapter, keyed by that
  adapter's address.
* Controller discovery is scoped to the operator's own adapter. The
  operator reads each HID device's `HID_PHYS` field and keeps only the
  devices whose controller is this adapter, so a second adapter's
  controllers would need a second scope.

A second adapter therefore needs a second Secret, a second discovery
scope, and a second set of published controllers. The current code
writes none of them.

## What breaks on a two-adapter node

The claim takes one adapter, and nothing in the claim chooses which.
The scheduler picks. The other adapter is unclaimed and unpublished, so
a controller paired to it carries no device, and a pod that selects it
stays Pending.

## Why this is not the audio answer

The audio operator serves every sound card on its node with
`allocationMode: All`, because one PipeWire spans every ALSA card and
its code already keys every output by card. bluetoothd could span
adapters the same way, but the per-adapter Secret and the per-adapter
discovery scope are the work that `allocationMode: All` would require
here, and that work is a design, not a claim edit.

## What is not measured

No liken machine has two Bluetooth adapters today. liken-1 has one. So
the cost of this limit has not been seen, and the shape of the answer
is a guess until a two-adapter machine runs the operator.
