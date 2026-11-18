---
title: Metrics
weight: 75
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` on port `9200`,
so your own Prometheus can keep the history that conditions do not:
how long each activation took, how often a device dropped its
connection, and how the camera's temperature and the guiding error
changed through a night. The base works without Prometheus in the
cluster. If you run the prometheus-operator, add the
`deploy/monitoring` component beside the base. It adds a `PodMonitor`
for the operator's pod and a Grafana dashboard:

```yaml
resources:
  - https://github.com/liken-sh/liken//observatory-operator/deploy?ref=<ref>
components:
  - https://github.com/liken-sh/liken//observatory-operator/deploy/monitoring?ref=<ref>
```

The dashboard is a `ConfigMap` in the `monitoring` namespace with the
label `grafana_dashboard: "1"`, which the Grafana sidecar of the
kube-prometheus-stack loads. If you run another Prometheus, scrape
port `9200` of the pod with the label `app: observatory-operator` in
`liken-system`.

Only the copy of the operator that holds the `Lease` serves metrics,
so during a rollout, scrapes of the new copy fail until it acquires
the `Lease`. The `OBSERVATORY_METRICS_ADDRESS` variable of the
`Deployment` sets the address, and an empty value turns the listener
off.

## The series

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| observatory-operator | `observatory_device_connected{namespace, kind, name}` | gauge | 1 while the device's driver reports `CONNECTION` on, 0 otherwise |
| observatory-operator | `observatory_device_claimed{namespace, kind, name}` | gauge | 1 while a reservation's session holds the device. A device that is claimed and not connected should raise an alert |
| observatory-operator | `observatory_device_disconnects_total{namespace, kind, name}` | counter | a driver that dropped its connection while a session held the device |
| observatory-operator | `observatory_observation_valid{namespace, source}`, `observatory_observation_last_success_timestamp_seconds{namespace, source}` | gauge | whether the operator has a connection to each INDI server and each PHD2, and when the source last sent a message |
| observatory-operator | `observatory_telescope_phase{namespace, telescope, phase}` | gauge | 1 for the phase of the telescope's reservation, or `Idle`, and 0 for each other phase |
| observatory-operator | `observatory_activation_duration_seconds{namespace, telescope}` | histogram | how long an activation took, from its first step after `Wait` to `Ready` |
| observatory-operator | `observatory_step_failures_total{namespace, telescope, step}` | counter | the steps of a reservation that failed, by the step's name |
| observatory-operator | `observatory_camera_temperature_celsius{namespace, name}` | gauge | the temperature that the camera's driver reports |
| observatory-operator | `observatory_camera_cooler_power_ratio{namespace, name}` | gauge | the cooler's power as a fraction of its maximum. A camera that cannot reach its temperature stays near 1 |
| observatory-operator | `observatory_guiding_error_arcseconds{namespace, guider, axis}` | gauge | PHD2's RMS error for the axes `ra`, `dec`, and `total` |
| observatory-operator | `observatory_guider_state{namespace, guider, state}` | gauge | 1 for PHD2's state, such as `Guiding` or `LostLock`, and 0 for each other state |
| observatory-operator | `observatory_reconcile_duration_seconds{kind}` | histogram | how long one pass of a loop took, by the kind of resource it serves |
| observatory-operator | `observatory_reconcile_errors_total{kind}` | counter | reconcile passes that returned an error, by the kind of resource that the loop reconciles |
| observatory-operator | `observatory_watch_restarts_total{kind}` | counter | watches that the API server closed and the operator opened again, by kind |
| observatory-operator | `liken_build_info{component, version}` | gauge | the release that the operator runs. The value is always 1 |

The labels name resources that you declared: the `namespace`, and the
`name` of a device, a `Telescope`, or a `Guider`. You create a new
`Reservation` for each night, so no series has a reservation's name
as a label, and the series about a reservation use the name of its
telescope. The `source` label of the observation series is the
resource that runs the INDI server, such as `Telescope/east` or
`Observatory/lab`, or the `Guider` that runs PHD2, such as
`Guider/east`.

The status writer sets the gauges of the devices, the telescopes, and
the guiders from the readings that it writes to their status, at most
once a second. So a driver that drops its connection and connects
again within that second does not count in
`observatory_device_disconnects_total`. A drop counts only while a
reservation's session holds the device, and not when the operator
disconnects the device itself, at the end of a session or after you
delete the device.

While the connection to a server is down,
`observatory_observation_valid` of the server is 0, and the gauges of
its devices keep their last values. The timestamp series gives the
age of those values. When the operator closes a connection because
the server's pod is gone, it deletes the observation series of that
source. When you delete a resource, its gauges disappear from the
scrape, and its counters stay.

A camera has the temperature series only while its driver reports
`CCD_TEMPERATURE`, and the cooler power series only while it reports
`CCD_COOLER_POWER`. The INDI camera simulator reports no cooler power.
A guider has the state series while it has a pod, and the guiding
error series only while PHD2 reports its pixel scale and has guide
steps to measure.

The `kind` label of the reconcile series is the kind of resource that
each loop serves:

* The supervisor counts under `Reservation`. It starts the runner of
  each reservation, and stops the servers that no reservation uses.
* The status writer counts under the kind of each status that it
  writes.
* The lock relay counts under `Observatory`, because it passes the
  park states between the dome and the mounts of each observatory.
* The trigger controller counts under the kind of each resource whose
  `spec.triggers` it runs.

## Alerts

The operator sends no notification. These rules suggest the alerts
that matter to a night of observing, for your own Prometheus and
Alertmanager to deliver:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: observatory
  namespace: monitoring
spec:
  groups:
    - name: observatory
      rules:
        # A session holds the device and its driver is not connected.
        # The session holds a device before its pod starts, and the
        # first pull of an image can take minutes, so the alert waits.
        - alert: ObservatoryDeviceNotConnected
          expr: |
            observatory_device_claimed == 1
              and on (namespace, kind, name) observatory_device_connected == 0
          for: 15m
          labels:
            severity: warning
          annotations:
            summary: "{{ $labels.kind }} {{ $labels.name }} in {{ $labels.namespace }} is reserved and not connected"
        # A step of a reservation failed. The reservation stays failed
        # until a person fixes the cause and runs the step again.
        - alert: ObservatoryStepFailed
          expr: increase(observatory_step_failures_total[10m]) > 0
          labels:
            severity: critical
          annotations:
            summary: "Step {{ $labels.step }} failed for Telescope {{ $labels.telescope }} in {{ $labels.namespace }}"
        # The cooler runs at full power and the camera has not reached
        # its temperature.
        - alert: ObservatoryCameraNotCooling
          expr: observatory_camera_cooler_power_ratio >= 0.99
          for: 20m
          labels:
            severity: warning
          annotations:
            summary: "Camera {{ $labels.name }} in {{ $labels.namespace }} has run its cooler at full power for 20 minutes"
        # PHD2 lost its guide star while guiding.
        - alert: ObservatoryGuideStarLost
          expr: observatory_guider_state{state="LostLock"} == 1
          for: 2m
          labels:
            severity: warning
          annotations:
            summary: "Guider {{ $labels.guider }} in {{ $labels.namespace }} lost its star"
```

[Troubleshoot](/docs/guides/troubleshoot/) shows how to find the cause of a failed step and run it again.
