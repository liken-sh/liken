---
title: Metrics
weight: 75
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` on port `9200`.
The base applies with no Prometheus in the cluster at all. If you run
the prometheus-operator, add the `deploy/monitoring` component beside
the base. It adds a `PodMonitor` for the operator's pod and a Grafana
dashboard:

```yaml
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<ref>
components:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy/monitoring?ref=<ref>
```

The dashboard is a `ConfigMap` in the `monitoring` namespace with the
label `grafana_dashboard: "1"`, which the Grafana sidecar of the
kube-prometheus-stack loads. If you run another Prometheus, scrape
port `9200` of the pod with the label `app: astrophotography-operator`
in `liken-system`.

Only the copy of the operator that holds the `Lease` serves metrics.
During a rollout, the scrape of the copy that waits fails until it
takes the `Lease`. The `ASTROPHOTOGRAPHY_METRICS_ADDRESS` variable of
the `Deployment` sets the address, and an empty value turns the
listener off.

## The series

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| astrophotography-operator | `astrophotography_reconcile_duration_seconds{kind}` | histogram | how long one pass of a loop took, by the kind of resource it serves: `Target`, `Solver`, or `Reservation` |
| astrophotography-operator | `astrophotography_reconcile_errors_total{kind}` | counter | passes of a loop that failed, by the kind of resource it serves |
| astrophotography-operator | `astrophotography_watch_restarts_total{kind}` | counter | watches that the API server closed and the operator opened again, by kind |
| astrophotography-operator | `liken_build_info{component, version}` | gauge | the release that the operator runs; always 1 |
| astrophotography-operator | `astrophotography_frames_archived_total` | counter | frames that promotion `Job`s moved from an outbox to an archive |
| astrophotography-operator | `astrophotography_promotion_bytes_total` | counter | bytes that promotion `Job`s copied from an outbox to an archive; a move within one claim copies none |
| astrophotography-operator | `astrophotography_promotion_failures_total{reason}` | counter | frames that a promotion `Job` failed to move, by `CopyFailed`, `VerifyFailed`, or `NameConflict` |

The promotion loop counts its passes under the kind `Target`. The
other kinds stay at zero until their loops are built. The three
promotion series count each `Job`'s report once, when the operator
applies it to the `Target`'s status ([The archive](/docs/concepts/archive/)).
