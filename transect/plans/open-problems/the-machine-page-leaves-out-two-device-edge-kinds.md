# The machine page leaves out two device edge kinds

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`parse` in `crates/transect-ground/src/devices/knowledge.rs` accepts a
`DeviceKnowledge` reference whose `edge` is any of: runs on, stores on,
holds, depends on, managed by, authenticates through, part of, and
reconciles from. `devices` in `crates/transect/src/web/pages/machine.rs`
lists a device's references from the edges of the kinds in
`DEVICE_EDGE_KINDS`, which holds only the first six.

So a reference with the edge `part of` or `reconciles from` draws an
edge in the graph, but the machine page does not list it under the
device. Plan 00, "Device knowledge", says that each device on a
machine's page lists "what its references name".

## What would confirm it

A test of the machine page with `DeviceKnowledge` that declares a
reference with `edge: part of` to an object that exists. The device's
lines do not name the object.

## What a fix has to decide

Whether the page lists every edge that device knowledge makes, or
whether `parse` refuses the edge kinds that a device cannot have.
