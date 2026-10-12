# 22, Prometheus metrics

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
answers what the design conversation left open.

Built on 2026-10-12; not drilled yet.

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

## Still open

The readings of the mount, the focuser, and the weather station could
each become a gauge. They wait until a dashboard needs them, because a
reading that nobody graphs costs a series on every scrape.
