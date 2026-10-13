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
| astrophotography-operator | `astrophotography_solves_total{result}` | counter | attempts to solve a frame, by their outcome: `solved`, `unsolved`, or `failed` when the worker or the solver answered an error |
| astrophotography-operator | `astrophotography_solve_duration_seconds` | histogram | how long a solver took to answer one star list, with the wait in its queue |
| astrophotography-operator | `astrophotography_watch_restarts_total{kind}` | counter | watches that the API server closed and the operator opened again, by kind |
| astrophotography-operator | `liken_build_info{component, version}` | gauge | the release that the operator runs; always 1 |
| astrophotography-operator | `astrophotography_frames_exposed_total{namespace, target}` | counter | frames that the driver saved in the outbox and the runner counted as pending, by target |
| astrophotography-operator | `astrophotography_exposure_failures_total{namespace, target}` | counter | exposures that gave no frame: the driver answered `Alert`, did not finish in time, saved the frame elsewhere, or the worker did not answer, by target |
| astrophotography-operator | `astrophotography_frame_preparation_seconds` | histogram | how long the settings before one exposure took: the upload, the frame type, the filter, and the keywords |
| astrophotography-operator | `astrophotography_centering_rounds` | histogram | rounds of solving and slewing that one centering took, whether it ended within the tolerance or gave up |
| astrophotography-operator | `astrophotography_centering_error_arcseconds{namespace, target}` | gauge | the angle between the last solved center and the target's coordinates, in arcseconds |
| astrophotography-operator | `astrophotography_dithers_total{namespace, target}` | counter | dithers that PHD2 settled after, by target |
| astrophotography-operator | `astrophotography_guiding_failures_total{namespace, target}` | counter | attempts to guide or dither that failed, such as no star found or a settle that timed out, by target |
| astrophotography-operator | `astrophotography_settle_seconds{operation}` | histogram | how long a `guide` or a `dither` took from the request to PHD2's `SettleDone`, calibration included |
| astrophotography-operator | `astrophotography_frames_archived_total` | counter | frames that promotion `Job`s moved from an outbox to an archive |
| astrophotography-operator | `astrophotography_promotion_bytes_total` | counter | bytes that promotion `Job`s copied from an outbox to an archive; a move within one claim copies none |
| astrophotography-operator | `astrophotography_promotion_failures_total{reason}` | counter | frames that a promotion `Job` failed to move, by `CopyFailed`, `VerifyFailed`, or `NameConflict` |

The promotion loop counts its passes under the kind `Target`, the
loop of the `Solver`s under the kind `Solver`, and the supervisor of
the runners and the loop of the workers under the kind `Reservation`.
The runner sets its three series when a frame is counted or fails,
and the three series of guiding when PHD2 settles or a guide or a
dither fails ([Imaging](/docs/concepts/imaging/)). The three
promotion series count each `Job`'s report once, when the operator
applies it to the `Target`'s status ([The archive](/docs/concepts/archive/)).
The runner's centering sets the two solve series and the two centering
series ([Imaging](/docs/concepts/imaging/#centering)).
