# Prometheus metrics

Plan 01. Built and drilled on liken-1 on 2026-09-10.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

The driver is small, and its one failure is a mount that does not
happen.

## The design

Layers 1 and 2 under the `per_node_csi_` prefix, on port 9200, with `kind`
as the CSI operation.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| per-node-csi-driver | `per_node_csi_volumes` | gauge | what is mounted |
| per-node-csi-driver | `per_node_csi_mount_failures_total` | counter | mounts that fail |
| per-node-csi-driver | `per_node_csi_copy_bytes{volume}` | gauge | the bytes each copy holds, from the walk `NodeGetVolumeStats` makes |

The monitoring component at `deploy/monitoring/` holds a PodMonitor for
each pod the driver runs.

## Proof

Failing tests first: one mount raises the gauge, one failure draws one
count.