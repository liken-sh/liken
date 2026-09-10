---
title: What the driver reports
weight: 30
---

## The listener

The node plugin serves its metrics at `/metrics` on the port named
`metrics`, `9200` by default, the port every process serves metrics on.
The base in `deploy/` needs no Prometheus and applies without one. A
cluster owner who runs the prometheus-operator adds the
`deploy/monitoring` component beside the base to scrape the pod:

```yaml
resources:
  - https://github.com/liken-sh/per-node-csi-driver//deploy?ref=<tag>
components:
  - https://github.com/liken-sh/per-node-csi-driver//deploy/monitoring?ref=<tag>
```

## The metrics

The names follow the contract in
[liken milestone 65](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md):
the runtime's own `go_*` and `process_*` series, `liken_build_info`, the
CSI operations as the reconcile layer, and the driver's own gauges.

| Metric | Labels | Meaning |
|---|---|---|
| `liken_build_info` | `component`, `version` | Always 1. The release this pod runs. |
| `pernodecsi_reconcile_duration_seconds` | `kind` | A histogram of each CSI call, by operation name. |
| `pernodecsi_reconcile_errors_total` | `kind` | CSI calls that returned an error, by operation name. |
| `pernodecsi_watch_restarts_total` | `kind` | Times the sweep's `PersistentVolume` watch closed and opened again. |
| `pernodecsi_volumes` | | The volumes this node holds a copy of. |
| `pernodecsi_mount_failures_total` | | Publishes that failed at the mount. |
| `per_node_copy_bytes` | `volume` | The bytes this node's copy of the volume holds, from the same walk `NodeGetVolumeStats` makes. The label is the volume handle. The gauge carries a volume from the first time the kubelet asks for its stats on that node until the pod unpublishes it or the sweep removes the copy. |

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
