---
title: What the driver reports
weight: 30
---

# What the driver reports

## The listener

The node plugin serves metrics at `/metrics` on the port named
`metrics`. The default port is `9200`, which every process in the base
uses for metrics.
The base in `deploy/` needs no Prometheus and applies without one. A
cluster owner who runs the prometheus-operator adds the
`deploy/monitoring` component beside the base to scrape the pod:

```yaml
resources:
  - https://github.com/liken-sh/liken//per-node-csi-driver/deploy?ref=<tag>
components:
  - https://github.com/liken-sh/liken//per-node-csi-driver/deploy/monitoring?ref=<tag>
```

## The metrics

The names follow the contract in
[liken milestone 65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md):
the runtime's own `go_*` and `process_*` series, `liken_build_info`, the
CSI operations as the reconcile layer, and the driver's own gauges.

| Metric | Labels | Meaning |
|---|---|---|
| `liken_build_info` | `component`, `version` | Always 1. The release this pod runs. |
| `per_node_csi_reconcile_duration_seconds` | `kind` | A histogram of each CSI call, by operation name. |
| `per_node_csi_reconcile_errors_total` | `kind` | CSI calls that returned an error, by operation name. |
| `per_node_csi_watch_restarts_total` | `kind` | Watches of `PersistentVolume`s the sweep opened again after one ended. The API server ends each watch after 5 to 10 minutes, so a healthy node counts 6 to 12 an hour. A higher rate is a watch that fails soon after it opens. A refused watch is not counted, so a rate near zero on a running node is a watch the API server refuses. |
| `per_node_csi_volumes` | | The volumes this node holds a copy of. |
| `per_node_csi_mount_failures_total` | | Publishes that failed at the mount. |
| `per_node_csi_copy_bytes` | `volume` | The bytes in this node's copy, from the walk that `NodeGetVolumeStats` makes. The label is the volume handle. The gauge starts when the kubelet first requests stats on that node and ends when the pod unpublishes the volume or the sweep removes the copy. |

## The kubelet's own numbers

The driver returns the copy's bytes as used space in
`NodeGetVolumeStats`. It returns the store's filesystem as available and
total space. The kubelet exports those values in its own metrics.
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
