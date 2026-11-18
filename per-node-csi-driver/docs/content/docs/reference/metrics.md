---
title: What the driver reports
weight: 30
---

# What the driver reports

## The listener

The node plugin serves metrics at `/metrics` on the port named
`metrics`. The default port is `9200`, which every `liken` process uses
for metrics. The base in `deploy/` does not need Prometheus. If you
run the prometheus-operator, add the `deploy/monitoring` component
beside the base so that Prometheus scrapes the pod:

```yaml
resources:
  - https://github.com/liken-sh/liken//per-node-csi-driver/deploy?ref=<tag>
components:
  - https://github.com/liken-sh/liken//per-node-csi-driver/deploy/monitoring?ref=<tag>
```

## The metrics

The node plugin serves the Go runtime's `go_*` and `process_*` series,
`liken_build_info`, and its own `per_node_csi_*` series. The
`per_node_csi_reconcile_*` series count and time the CSI calls that the
kubelet makes, and use the name that other `liken` components use for
their main loop.

| Metric | Labels | Meaning |
|---|---|---|
| `liken_build_info` | `component`, `version` | Always 1. The release that this pod runs. |
| `per_node_csi_reconcile_duration_seconds` | `kind` | A histogram of the time each CSI call takes. `kind` is the name of the call, such as `NodePublishVolume`. |
| `per_node_csi_reconcile_errors_total` | `kind` | The CSI calls that returned an error, by the name of the call. |
| `per_node_csi_watch_restarts_total` | `kind` | The number of times the node plugin opened its watch on `PersistentVolume`s again after the watch ended. `kind` is `PersistentVolume`. client-go sets a random timeout of 5 to 10 minutes on each watch request, and the API server ends the watch at that timeout, so a healthy node counts 6 to 12 an hour. A higher rate means that watches end soon after they open. A watch that the API server refuses is not counted, so a rate near zero on a running node can mean that the API server refuses the watch. |
| `per_node_csi_volumes` | | The volumes that at least one pod on this node has published. |
| `per_node_csi_mount_failures_total` | | The publishes that failed because the driver could not create the copy or the target, or could not bind-mount the copy at the target. |
| `per_node_csi_copy_bytes` | `volume` | The size in bytes of this node's copy, which the driver measures by walking the copy each time `NodeGetVolumeStats` is called. `volume` is the volume handle. The series appears when the kubelet first requests statistics for the volume on that node. It disappears when the last pod on the node unpublishes the volume, or when the driver removes the copy. |

## The kubelet's volume statistics

In its `NodeGetVolumeStats` response, the driver reports the size of the
copy as used space, and the filesystem that contains the store as
available and total space. The kubelet exports these values in its own
metrics. `kubelet_volume_stats_used_bytes` is the size of the copy.
`kubelet_volume_stats_available_bytes` and
`kubelet_volume_stats_capacity_bytes` describe the filesystem, which
every volume on the node shares. On `liken` that filesystem is the
pod-storage partition.

## The log

The driver logs one line for each CSI call, with the call's name and
status code. It also logs one line for each publish, with the handle and
the target, one for each unpublish, with the handle, and one for each
copy that it removes because its `PersistentVolume` is gone. It logs a
warning when it cannot write, read, or remove its record of a pod's
mount of a copy, when it cannot post an `Event`, and when it cannot
remove a copy. When the driver has no Kubernetes client, it logs a
warning at start and never removes a copy.
