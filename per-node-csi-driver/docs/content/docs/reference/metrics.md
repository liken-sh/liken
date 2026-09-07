---
title: What the driver reports
weight: 30
---

## The gauge

The node plugin serves one gauge at `/metrics` on the port named
`metrics`, `9808` by default. The gauge carries a volume from the first
time the kubelet asks for its stats on that node until the pod
unpublishes it or the sweep removes the copy.

| Metric | Labels | Meaning |
|---|---|---|
| `per_node_copy_bytes` | `volume` | The bytes this node's copy of the volume holds, from the same walk `NodeGetVolumeStats` makes. The label is the volume handle. |

## The kubelet's own numbers

The driver answers `NodeGetVolumeStats` with the copy's bytes as used,
and the filesystem that holds the store as available and total. The
kubelet carries those numbers on its own metrics.
`kubelet_volume_stats_used_bytes` is the copy.
`kubelet_volume_stats_available_bytes` and
`kubelet_volume_stats_capacity_bytes` are the filesystem, which every
volume on the node shares. On `liken` that filesystem is the
pod-storage partition.

## The log

The driver writes one line per RPC with its name and its status code,
because the kubelet's calls are the driver's whole input. It writes one
line per publish, with the handle and the target, and one line per
unpublish, with the handle. It writes one line per copy the sweep
removed. A warning names a hold the driver could not write, read, or
remove, an event it could not post, and a copy it could not remove.
