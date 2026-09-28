# Prometheus metrics

Plan 13. Built and drilled on liken-1 on 2026-09-10.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

The forge can be slow or down, and the store can grow on the wrong
filesystem. Both are open problems in this repository, and both are
graphs before they are fixes.

## The design

Layers 1 and 2 under the `git_csi_` prefix, on port 9200. The driver's
CSI calls are its reconcile loop: `kind` is the CSI operation.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| git-csi-driver | `git_csi_volumes{repo}` | gauge | what is mounted |
| git-csi-driver | `git_csi_fetch_duration_seconds{repo}` | histogram | the forge is slow |
| git-csi-driver | `git_csi_fetch_failures_total{repo}` | counter | the forge is down |
| git-csi-driver | `git_csi_store_bytes` | gauge | growth on the wrong filesystem |

The `repo` label is a short name the driver already has for a
repository, never a URL with credentials. The monitoring component at
`deploy/monitoring/` holds a PodMonitor for each pod the driver runs.
This plan answers the open problem "Monitoring".

## Proof

Failing tests first. In the lab, mount a volume against an unreachable
forge and read the failure counter climb.
