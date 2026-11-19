# The machine page leaves out two device edge kinds

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test read each edge phrase that a `DeviceKnowledge` reference
accepts with `edge_kind`, the function that `parse` uses, and wanted
each edge kind in `DEVICE_EDGE_KINDS` of
`crates/transect/src/web/pages/machine.rs`. The cases `part of` and
`reconciles from` failed:

```
a reference with edge "part of" draws PartOf, which the machine page leaves out of the device's lines, want it listed
a reference with edge "reconciles from" draws ReconcilesFrom, which the machine page leaves out of the device's lines, want it listed
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should a device on a machine's page list every reference that its
`DeviceKnowledge` declares, or should `DeviceKnowledge` refuse the
edges `part of` and `reconciles from`?

Whether the page lists every edge that device knowledge makes, or
whether `parse` refuses the edge kinds that a device cannot have.
