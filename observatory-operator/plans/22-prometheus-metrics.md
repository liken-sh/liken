# 22, Prometheus metrics

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
answers what the design conversation left open.

Built on 2026-10-12. Drilled on the QEMU dev cluster on 2026-10-12,
with no guider; [the drill](#the-drill) records the results.

## The problem

[Root plan 65](../../plans/completed/65-prometheus-metrics.md) says
every long-running process that a `liken` operator ships serves
Prometheus metrics, under one contract for the port, the names, and the
labels. `observatory-operator` serves none today. Conditions say what
is true now, and nothing keeps the history: how long activation took,
how often a device dropped its connection, how the guiding error moved
through a night. Nobody watches the cluster at dawn, so a condition
that waits on a person also needs a way to reach that person.

## Decisions

- **Conditions come first.** Each state that a person or a controller
  acts on is a condition on the operator's resources, as the
  repository's rule for conditions and `Event`s requires.
- **The operator exports its own metrics under plan 65's contract.**
  Plan 65 set aside kube-state-metrics on the CRDs, because it names
  series in its own vocabulary and adds a second reader between the
  fact and the graph.
- **Alerts reach a person through their own Prometheus and
  Alertmanager.** The operator exports the series, and the manual
  suggests the alerts, such as a telescope that waits for someone to
  set it up or to cover it ([plan 21](21-a-person-in-the-procedure.md)).
  The operator sends no notification itself.

## The design

The operator follows root plan 65 in every layer: the port is 9200 and
named `metrics`, the prefix is `observatory_`, a scrape reads an
in-memory registry and never touches INDI, PHD2, or the API, and a
failure of the listener never stops the operator's work. Labels are
bounded by inventory: the namespace and the name of a resource that a
person declared, a kind, a step, or a phase. A reservation is not
inventory, because a person creates one for each night, so no series
carries a reservation's name. The series of a reservation carry the
name of its telescope.

| Series | Type | Why |
| --- | --- | --- |
| `liken_build_info{component="observatory-operator", version}` | gauge, info | the release that runs |
| `observatory_reconcile_duration_seconds{kind}`, `observatory_reconcile_errors_total{kind}`, `observatory_watch_restarts_total{kind}` | histogram, counters | plan 65's second layer; `kind` is the kind of the resource each loop serves |
| `observatory_device_connected{namespace, kind, name}` | gauge, 1 or 0 | the driver reports `CONNECTION` on |
| `observatory_device_claimed{namespace, kind, name}` | gauge, 1 or 0 | a session holds the device; claimed and not connected is the alert |
| `observatory_observation_valid{namespace, source}` and `observatory_observation_last_success_timestamp_seconds{namespace, source}` | gauge, gauge | the connection to each INDI server and each PHD2; `source` is the server's resource, such as `Telescope/east`. Plan 65 gives these two `source` alone; `namespace` is added because two namespaces can each have a `Telescope/east` |
| `observatory_telescope_phase{namespace, telescope, phase}` | gauge, 1 or 0 | the phase of the telescope's reservation, or `Idle` |
| `observatory_activation_duration_seconds{namespace, telescope}` | histogram | from the start of activation to `Ready` |
| `observatory_step_failures_total{namespace, telescope, step}` | counter | a step that fails, by its name |
| `observatory_device_disconnects_total{namespace, kind, name}` | counter | a driver that drops its connection while a session holds it |
| `observatory_camera_temperature_celsius{namespace, name}` and `observatory_camera_cooler_power_ratio{namespace, name}` | gauges | the cooling of a night, a camera that cannot reach its temperature |
| `observatory_guiding_error_arcseconds{namespace, guider, axis}` | gauge | PHD2's RMS error, for `ra`, `dec`, and `total` |
| `observatory_guider_state{namespace, guider, state}` | gauge, 1 or 0 | PHD2's state, such as `Guiding` or `LostLock` |

- **The series come from what the operator already reads.** The status
  writer holds each device's readings and the guider's state, so the
  gauges follow the same stores, and a gauge of a deleted resource is
  deleted with it.
- **`deploy/monitoring/` is a kustomize component**, as plan 65 gives
  every repository: a `PodMonitor` for the operator's pod and a
  dashboard. The base applies on a cluster with no monitoring.
- **The manual suggests the alerts** as a `PrometheusRule`: a device
  claimed and not connected, a step that failed, a camera that does not
  reach its temperature, and guiding that lost its star. [Plan
  21](21-a-person-in-the-procedure.md) adds the series and the alert
  for a telescope that waits on a person.

## The drill

The drill ran on 2026-10-12 on the QEMU dev cluster, on the lab of
[plan 19's drill](19-the-frames-outbox.md#the-drill), beside the
drills of [plan 18](18-saved-horizons.md#the-drill) and [plan
20](20-declared-fits-keywords.md#the-drill). The operator was built
from d8d0759a and reached the lab through the lab registry as
`ghcr.io/liken-sh/observatory-operator:dev`. A port forward to the
operator pod's port 9200 read `/metrics` when a reservation changed
its step, and between the checks. The observatory was a copy of
`examples/simulators.yaml` without its `Reservation`, with 17 devices.

| Check | Result |
|---|---|
| Read `/metrics` with no reservation | `liken_build_info{component="observatory-operator",version="dev"} 1`. Each of the 20 kinds had `reconcile_duration_seconds`, and `reconcile_errors_total` and `watch_restarts_total` at 0. Every device had `device_connected` and `device_claimed` at 0, every `step_failures_total` was 0, and both telescopes had `telescope_phase` `Idle` at 1. |
| Reserve `east` and read at each step | `telescope_phase` was `Idle` in the scrape at `Wait`, `Activating` from `StartSite` through `Activation`, and `Ready` when the reservation was `Ready`. |
| Read while `east` is `Ready` | The 14 devices of the session, the dome and the weather station included, had `device_connected` and `device_claimed` at 1. `activation_duration_seconds` had a count of 1 and a sum of 35.9 s, and the reservation was `Ready` 35 s after its creation. `observation_valid` was 1 for `Observatory/lab` and `Telescope/east`, each with a timestamp of its last success. `camera_temperature_celsius` was -10 for `east-main` and 25 for `east-guide`, which does not cool. |
| Delete the pod `east-main-camera` | `device_disconnects_total` of `east-main` went from 0 to 1. After the new driver connected, `camera_temperature_celsius` was -4, and the next frame's `CCD-TEMP` was -10. |
| Let `Configure` fail on `east`, and fail `PowerOn` on `west` with the outbox `nope`, a claim that does not exist | `step_failures_total` was 1 for `Configure` on `east` and 1 for `PowerOn` on `west`, and `telescope_phase` was `Error` at 1 for each. `west` was `Idle` again 3 s after the delete of its reservation. |
| End the reservation, and read at each step | When `Disconnect` started, 63 s after the delete, `device_connected` of `east-main` was 0 and `device_claimed` was 1, and the camera's temperature gauges were gone. At `StopSite`, `observation_valid` of `Observatory/lab` was 0, and the series of `Telescope/east` were gone. After the release, every device was at 0 for both, and the observation series were gone. |
| Reserve `east` again | `activation_duration_seconds` had a count of 2 and a sum of 71.9 s. |
| Delete `Focuser spare` | Within 4 s, its `device_connected` and `device_claimed` were gone, and `device_disconnects_total` stayed, as `series.go` intends for a counter. Applying the example again brought all three back. |
| Read the series of each loop after about 15 minutes | `reconcile_errors_total` was 0 for every kind. `watch_restarts_total` was 1 or 2 for each kind, because client-go's reflector opens each watch again after 5 to 10 minutes. `reconcile_duration_seconds` of `Horizon` stayed at a count of 0, because a `Horizon` has no status and no loop writes one. |
| Render the base with the `deploy/monitoring` component through `kubectl kustomize` | One `PodMonitor` in `liken-system`, which selects `app: observatory-operator` on the port named `metrics`, and the `ConfigMap` `observatory-operator-dashboard` in `monitoring` with `grafana_dashboard: "1"`. The running pod had that label and that port. Every series that the dashboard names is one that `metrics.go` defines. The lab has no prometheus-operator, so the drill did not apply the component. |

Three series did not appear. `camera_cooler_power_ratio` has no value,
because the CCD simulator defines no `CCD_COOLER_POWER`.
`guider_state` and `guiding_error_arcseconds` have no value, because
no PHD2 ran. The guider's pod runs beside the telescope's server on
`node-4`, whose image filesystem had 450 MiB free, and the images of
PHD2 and weston take about 785 MB when unpacked. A reservation with
`Guider east` showed no guider series up to `Activation`, where the
drill ended it before `StartGuider`. The guider's series wait for a
drill on a cluster with room for those images.

## Still open

The readings of the mount, the focuser, and the weather station could
each become a gauge. They wait until a dashboard needs them, because a
reading that nobody graphs costs a series on every scrape.
